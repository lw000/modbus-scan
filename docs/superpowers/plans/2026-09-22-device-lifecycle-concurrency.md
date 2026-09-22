# Device Lifecycle Concurrency Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reject overlapping start, stop, and restart requests for the same device with HTTP 409 while keeping different devices concurrent and synchronizing lifecycle controls across browser pages.

**Architecture:** Keep the existing per-device runtime entry and mutex, but use non-blocking acquisition for interactive lifecycle methods. Expose the active operation in runtime snapshots, map a stable busy sentinel to the HTTP API, and let both pages disable all lifecycle buttons from that snapshot. Internal startup and shutdown paths retain blocking acquisition so graceful shutdown cannot skip a device.

**Tech Stack:** Go 1.21, `sync.Mutex.TryLock`, Gin, embedded vanilla JavaScript, Go `testing`/`httptest`/`fstest`.

## Global Constraints

- Preserve the current single-process Windows service deployment model; do not add distributed locks or browser-only locking.
- Do not add third-party dependencies.
- Use TDD for every behavior change and deterministic channel-based concurrency tests; do not use arbitrary `time.Sleep` calls.
- Keep `StartEnabled` and `StopAll` blocking; only interactive `Start`, `Stop`, and `Restart` return `ErrDeviceBusy`.
- A rejected busy request must have no database, runner, enabled-state, or runtime-state side effects.
- Preserve the existing uncommitted stopped-device restart fix in `internal/runtime/manager.go` and its regression test in `internal/runtime/manager_test.go`.
- Preserve existing lifecycle URLs and successful response bodies.
- Return busy responses as HTTP 409 with code `device_busy` and message `设备正在操作，请稍后重试`.
- Finish with `gofmt`, `go test -race ./...`, `go test ./...`, and `go vet ./...`.

---

## File Structure

- Modify `internal/runtime/manager.go`: define the busy sentinel, track the active operation, and separate interactive non-blocking acquisition from internal blocking acquisition.
- Modify `internal/runtime/manager_test.go`: add deterministic same-device, cross-device, snapshot, cleanup, and shutdown concurrency coverage.
- Modify `internal/httpapi/response.go`: map the runtime busy sentinel to the public 409 error contract.
- Modify `internal/httpapi/httpapi_test.go`: exercise the lifecycle endpoint response through the real router.
- Modify `web/js/api.js`: preserve the server error code on thrown JavaScript errors.
- Modify `web/js/devices.js`: disable all lifecycle controls for busy rows and refresh after a conflict.
- Modify `web/js/device.js`: disable all detail-page lifecycle controls while an operation is active and refresh after a conflict.
- Modify `web/embed_test.go`: assert the embedded scripts contain the new concurrency behavior.
- Modify `README.md`: document the snapshot field and 409 behavior.

### Task 1: Runtime Non-Blocking Lifecycle Guard

**Files:**
- Modify: `internal/runtime/manager.go`
- Test: `internal/runtime/manager_test.go`

**Interfaces:**
- Produces: `var ErrDeviceBusy error`
- Produces: `Snapshot.Operation string` serialized as `operation,omitempty`
- Produces: interactive methods `Start(context.Context, int64) error`, `Stop(context.Context, int64) error`, and `Restart(context.Context, int64) error` returning `ErrDeviceBusy` when the same device is occupied.
- Preserves: `StartEnabled(context.Context) []error` and `StopAll(context.Context) error` blocking until the device mutex is available.

- [ ] **Step 1: Add deterministic test hooks to the fake source**

Extend `fakeSource` so tests can block `SetDeviceEnabled` independently by device ID without sleeping:

```go
type fakeSource struct {
	mu              sync.Mutex
	devices         map[int64]model.Device
	points          map[int64][]model.Point
	lifecycleBlocks map[int64]*lifecycleBlock
}

type lifecycleBlock struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (f *fakeSource) SetDeviceEnabled(_ context.Context, id int64, enabled bool) (model.Device, error) {
	f.mu.Lock()
	block := f.lifecycleBlocks[id]
	f.mu.Unlock()
	if block != nil {
		block.once.Do(func() { close(block.entered) })
		<-block.release
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.devices[id]
	d.Enabled = enabled
	f.devices[id] = d
	return d, nil
}
```

Initialize `lifecycleBlocks` to an empty map in `newTestManager`. Tests install a block only for the device that must pause.

- [ ] **Step 2: Write failing tests for same-device rejection and operation visibility**

Add a table-driven test that starts one operation in a goroutine, waits for `setEnabledEntered`, checks the snapshot, then invokes each competing operation:

```go
func TestInteractiveLifecycleRejectsBusyDevice(t *testing.T) {
	tests := []struct {
		name string
		call func(*Manager) error
	}{
		{name: "start", call: func(m *Manager) error { return m.Start(context.Background(), 1) }},
		{name: "stop", call: func(m *Manager) error { return m.Stop(context.Background(), 1) }},
		{name: "restart", call: func(m *Manager) error { return m.Restart(context.Background(), 1) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, source, factory := newTestManager()
			block := &lifecycleBlock{entered: make(chan struct{}), release: make(chan struct{})}
			source.lifecycleBlocks[1] = block
			firstDone := make(chan error, 1)
			go func() { firstDone <- m.Start(context.Background(), 1) }()
			<-block.entered

			snapshot, err := m.Snapshot(context.Background(), 1)
			if err != nil || snapshot.Operation != "start" {
				t.Fatalf("snapshot=%#v err=%v", snapshot, err)
			}
			if err := tt.call(m); !errors.Is(err, ErrDeviceBusy) {
				t.Fatalf("error=%v, want ErrDeviceBusy", err)
			}
			if factory.count != 0 {
				t.Fatalf("runner count=%d before release", factory.count)
			}

			close(block.release)
			if err := <-firstDone; err != nil {
				t.Fatal(err)
			}
			snapshot, err = m.Snapshot(context.Background(), 1)
			if err != nil || snapshot.Operation != "" {
				t.Fatalf("snapshot=%#v err=%v", snapshot, err)
			}
			_ = m.Stop(context.Background(), 1)
		})
	}
}
```

Because `Snapshot` currently has no `Operation` field and lifecycle calls block, this test must fail to compile or hang before implementation. Use a short `context.WithTimeout` around the test only if needed to prevent a broken implementation from hanging the suite; do not use sleeps to order events.

- [ ] **Step 3: Run the focused test and verify RED**

Run:

```powershell
$env:GOCACHE=(Join-Path (Get-Location) '.gocache')
go test ./internal/runtime -run TestInteractiveLifecycleRejectsBusyDevice -count=1
```

Expected: FAIL because `Snapshot.Operation` and `ErrDeviceBusy` do not exist, or because the competing request blocks instead of returning.

- [ ] **Step 4: Implement the busy sentinel, operation field, and acquisition helpers**

Add the public error and snapshot field:

```go
var ErrDeviceBusy = errors.New("device is busy")

type Snapshot struct {
	DeviceID            int64                `json:"device_id"`
	State               string               `json:"state"`
	Operation           string               `json:"operation,omitempty"`
	LastError           string               `json:"last_error,omitempty"`
	LastCollectedAt     *time.Time           `json:"last_collected_at,omitempty"`
	LoadedConfigVersion int64                `json:"loaded_config_version"`
	ConfigPending       bool                 `json:"config_pending"`
	Values              map[string]udm.Value `json:"values,omitempty"`
}
```

Add `operation string` beside `state` in `deviceRuntime`, then add helpers that keep operation updates under `entry.mu`:

```go
func (entry *deviceRuntime) tryBeginOperation(operation string) bool {
	if !entry.opMu.TryLock() {
		return false
	}
	entry.mu.Lock()
	entry.operation = operation
	entry.mu.Unlock()
	return true
}

func (entry *deviceRuntime) endOperation() {
	entry.mu.Lock()
	entry.operation = ""
	entry.mu.Unlock()
	entry.opMu.Unlock()
}
```

Use the helper at the start of each interactive method:

```go
if !entry.tryBeginOperation("start") {
	return ErrDeviceBusy
}
defer entry.endOperation()
```

Use `"stop"` and `"restart"` for the other methods. Keep `StartEnabled` and `StopAll` on their existing blocking `entry.opMu.Lock()` paths. Copy `entry.operation` into `Snapshot.Operation` while holding `entry.mu.RLock()`.

- [ ] **Step 5: Run the focused test and verify GREEN**

Run:

```powershell
$env:GOCACHE=(Join-Path (Get-Location) '.gocache')
go test ./internal/runtime -run TestInteractiveLifecycleRejectsBusyDevice -count=1
```

Expected: PASS.

- [ ] **Step 6: Add tests for different-device concurrency and blocking shutdown**

Add a second device and prove device 2 starts before device 1 is released:

```go
func TestInteractiveLifecycleAllowsDifferentDevices(t *testing.T) {
	m, source, factory := newTestManager()
	source.devices[2] = model.Device{ID: 2, Name: "plc-2", ConfigVersion: 1}
	block := &lifecycleBlock{entered: make(chan struct{}), release: make(chan struct{})}
	source.lifecycleBlocks[1] = block
	firstDone := make(chan error, 1)
	go func() { firstDone <- m.Start(context.Background(), 1) }()
	<-block.entered

	if err := m.Start(context.Background(), 2); err != nil {
		t.Fatalf("start device 2: %v", err)
	}
	if factory.count != 1 {
		t.Fatalf("runner count=%d, want device 2 to start while device 1 is blocked", factory.count)
	}
	close(block.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if factory.count != 2 {
		t.Fatalf("runner count=%d, want 2", factory.count)
	}
	_ = m.StopAll(context.Background())
}
```

Add a shutdown test whose first `Start` owns the operation lock while blocked, starts `StopAll` in another goroutine, verifies `StopAll` has not returned, releases the first call, and then asserts `StopAll` returns and the snapshot is stopped:

```go
func TestStopAllWaitsForInteractiveOperation(t *testing.T) {
	m, source, _ := newTestManager()
	block := &lifecycleBlock{entered: make(chan struct{}), release: make(chan struct{})}
	source.lifecycleBlocks[1] = block
	startDone := make(chan error, 1)
	go func() { startDone <- m.Start(context.Background(), 1) }()
	<-block.entered

	stopAllStarted := make(chan struct{})
	stopAllDone := make(chan error, 1)
	go func() {
		close(stopAllStarted)
		stopAllDone <- m.StopAll(context.Background())
	}()
	<-stopAllStarted

select {
case err := <-stopAllDone:
	t.Fatalf("StopAll returned before lifecycle operation completed: %v", err)
default:
}
	close(block.release)
	if err := <-startDone; err != nil {
		t.Fatal(err)
	}
	if err := <-stopAllDone; err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.Snapshot(context.Background(), 1)
	if err != nil || snapshot.State != StateStopped {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
}
```

Extend `fakeFactory` with `err error` and return it before creating a runner:

```go
type fakeFactory struct {
	mu    sync.Mutex
	count int
	err   error
}

func (f *fakeFactory) New(_ model.Device, _ []model.Point, sink StatusSink) (Runner, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	f.count++
	sink(StatusEvent{State: StateOnline, At: time.Now()})
	return newFakeRunner(), nil
}
```

Add this cleanup regression:

```go
func TestInteractiveLifecycleReleasesOperationAfterFailure(t *testing.T) {
	m, _, factory := newTestManager()
	factory.err = errors.New("factory failed")
	if err := m.Start(context.Background(), 1); err == nil {
		t.Fatal("expected start failure")
	}
	factory.err = nil
	if err := m.Start(context.Background(), 1); err != nil {
		t.Fatalf("retry start: %v", err)
	}
	_ = m.Stop(context.Background(), 1)
}
```

- [ ] **Step 7: Run runtime tests with the race detector**

Run:

```powershell
$env:GOCACHE=(Join-Path (Get-Location) '.gocache')
go test -race ./internal/runtime -count=1
```

Expected: PASS with no race reports.

- [ ] **Step 8: Format and commit the runtime unit**

Run:

```powershell
gofmt -w internal/runtime/manager.go internal/runtime/manager_test.go
git add internal/runtime/manager.go internal/runtime/manager_test.go
git commit -m "feat: reject concurrent device lifecycle operations"
```

Expected: commit succeeds and includes the previously completed stopped-device restart fix plus the new concurrency behavior.

### Task 2: HTTP Busy Error Contract

**Files:**
- Modify: `internal/httpapi/response.go`
- Test: `internal/httpapi/httpapi_test.go`

**Interfaces:**
- Consumes: `runtime.ErrDeviceBusy` from Task 1.
- Produces: HTTP 409 error envelope with code `device_busy` and message `设备正在操作，请稍后重试`.

- [ ] **Step 1: Make the device stub return lifecycle errors**

Extend the existing stub without changing successful tests:

```go
type stubDevices struct {
	created     model.Device
	startCalls  int
	lifecycleErr error
}

func (s *stubDevices) Start(context.Context, int64) error {
	s.startCalls++
	return s.lifecycleErr
}
func (s *stubDevices) Stop(context.Context, int64) error    { return s.lifecycleErr }
func (s *stubDevices) Restart(context.Context, int64) error { return s.lifecycleErr }
```

- [ ] **Step 2: Write the failing HTTP contract test**

```go
func TestDeviceLifecycleBusyReturnsConflict(t *testing.T) {
	devices := &stubDevices{lifecycleErr: devruntime.ErrDeviceBusy}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/1/restart", nil)
	rec := httptest.NewRecorder()
	testRouter(devices, &stubPoints{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, `"code":"device_busy"`) || !strings.Contains(body, `"message":"设备正在操作，请稍后重试"`) {
		t.Fatalf("body=%s", body)
	}
}
```

- [ ] **Step 3: Run the focused test and verify RED**

Run:

```powershell
$env:GOCACHE=(Join-Path (Get-Location) '.gocache')
go test ./internal/httpapi -run TestDeviceLifecycleBusyReturnsConflict -count=1
```

Expected: FAIL with HTTP 500 and code `internal_error`.

- [ ] **Step 4: Add the busy mapping before the generic store conflict mapping**

Import the runtime package with the existing alias convention and add this case to `handleError`:

```go
case errors.Is(err, devruntime.ErrDeviceBusy):
	failure(c, http.StatusConflict, "device_busy", "设备正在操作，请稍后重试", nil)
```

Keep `store.ErrConflict` mapped to the existing generic `conflict` response.

- [ ] **Step 5: Run HTTP tests and commit**

Run:

```powershell
gofmt -w internal/httpapi/response.go internal/httpapi/httpapi_test.go
$env:GOCACHE=(Join-Path (Get-Location) '.gocache')
go test ./internal/httpapi -count=1
git add internal/httpapi/response.go internal/httpapi/httpapi_test.go
git commit -m "feat: return conflict for busy devices"
```

Expected: all `internal/httpapi` tests PASS and the commit succeeds.

### Task 3: Multi-Page Lifecycle Controls

**Files:**
- Modify: `web/js/api.js`
- Modify: `web/js/devices.js`
- Modify: `web/js/device.js`
- Test: `web/embed_test.go`

**Interfaces:**
- Consumes: `runtime.operation` from Task 1 and `error.code == "device_busy"` from Task 2.
- Produces: JavaScript errors with `code`, row/detail lifecycle buttons disabled while busy, and immediate refresh after a busy response.

- [ ] **Step 1: Write failing embedded-resource assertions**

Add focused tests instead of expanding the unrelated point-table assertion:

```go
func TestLifecycleScriptsHandleBusyDevices(t *testing.T) {
	apiData, err := fs.ReadFile(Assets, "js/api.js")
	if err != nil {
		t.Fatal(err)
	}
	apiScript := string(apiData)
	if !strings.Contains(apiScript, "error.code") || !strings.Contains(apiScript, "body?.error?.code") {
		t.Error("api errors do not preserve the server error code")
	}

	listData, err := fs.ReadFile(Assets, "js/devices.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"r.operation", "data-lifecycle", "device_busy", "await load()"} {
		if !strings.Contains(string(listData), required) {
			t.Errorf("devices.js missing %q", required)
		}
	}

	detailData, err := fs.ReadFile(Assets, "js/device.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"function setLifecycleBusy", "runtime.operation", "device_busy", "await loadStatus()"} {
		if !strings.Contains(string(detailData), required) {
			t.Errorf("device.js missing %q", required)
		}
	}
}
```

- [ ] **Step 2: Run the focused asset test and verify RED**

Run:

```powershell
$env:GOCACHE=(Join-Path (Get-Location) '.gocache')
go test ./web -run TestLifecycleScriptsHandleBusyDevices -count=1
```

Expected: FAIL for all missing busy-control markers.

- [ ] **Step 3: Preserve API error codes**

In `web/js/api.js`, extend the non-OK branch to retain the stable server code:

```js
if(!response.ok){
  const error=new Error(body?.error?.message||`HTTP ${response.status}`);
  error.code=body?.error?.code;
  error.details=body?.error?.details;
  throw error
}
```

Keep the request methods and response unwrapping unchanged.

- [ ] **Step 4: Disable all lifecycle buttons on the list page**

When rendering each row in `web/js/devices.js`, derive `const operating=Boolean(r.operation)` and render lifecycle buttons with `data-lifecycle` and `disabled` when operating. Do not disable Edit or Delete solely because collection lifecycle is busy:

```js
const operating=Boolean(r.operation);
// In the actions cell:
`<button data-lifecycle data-op="${d.enabled?'stop':'start'}" data-id="${d.id}" ${operating?'disabled':''}>${d.enabled?'停止':'启动'}</button>`
`<button data-lifecycle data-op="restart" data-id="${d.id}" ${operating?'disabled':''}>重启</button>`
```

Before posting, disable every lifecycle button for the same device:

```js
const lifecycleButtons=[...tbody.querySelectorAll(`[data-lifecycle][data-id="${b.dataset.id}"]`)];
lifecycleButtons.forEach(button=>button.disabled=true);
```

In `catch`, show the error and refresh immediately for a busy conflict:

```js
catch(err){
  showError(err);
  if(err.code==="device_busy") await load();
}
```

The existing successful path already calls `load()`. Avoid a `finally` block that blindly enables buttons, because the refreshed snapshot remains authoritative.

- [ ] **Step 5: Disable all lifecycle buttons on the detail page**

Add a focused helper to `web/js/device.js`:

```js
function setLifecycleBusy(busy) {
  document.querySelectorAll("#controls [data-action]").forEach(button => {
    button.disabled = busy;
  });
}
```

At the end of `renderRuntime(runtime)`, call:

```js
setLifecycleBusy(Boolean(runtime.operation));
```

In the controls click handler, call `setLifecycleBusy(true)` before the request. On success call `refresh()`. On `device_busy`, show the error and call `await loadStatus()` immediately. In `finally`, do not force-enable controls; the latest snapshot must decide whether they remain disabled.

- [ ] **Step 6: Run web tests and commit**

Run:

```powershell
$env:GOCACHE=(Join-Path (Get-Location) '.gocache')
go test ./web -count=1
git add web/js/api.js web/js/devices.js web/js/device.js web/embed_test.go
git commit -m "feat: synchronize device lifecycle controls"
```

Expected: all `web` tests PASS and the commit succeeds.

### Task 4: Documentation and Full Verification

**Files:**
- Modify: `README.md`

**Interfaces:**
- Documents: `Snapshot.operation`, same-device immediate rejection, HTTP 409 error shape, and polling behavior.

- [ ] **Step 1: Update the runtime status and lifecycle API documentation**

Add the following content near the existing device states and start/stop/restart endpoint description:

````markdown
`runtime.operation` 表示当前生命周期操作，可为 `start`、`stop`、`restart`；字段不存在或为空表示当前没有操作。同一设备正在执行生命周期操作时，后续启动、停止或重启请求不会排队，而是返回 HTTP `409 Conflict`：

```json
{"error":{"code":"device_busy","message":"设备正在操作，请稍后重试"}}
```

设备列表页和详情页通过状态轮询同步该字段并禁用生命周期按钮。不同设备之间仍可并行操作。
````

- [ ] **Step 2: Run formatting and whitespace checks**

Run:

```powershell
gofmt -w internal/runtime/manager.go internal/runtime/manager_test.go internal/httpapi/response.go internal/httpapi/httpapi_test.go
git diff --check
```

Expected: no output from `git diff --check`.

- [ ] **Step 3: Run the complete race-enabled suite**

Run:

```powershell
$env:GOCACHE=(Join-Path (Get-Location) '.gocache')
go test -race ./... -count=1
```

Expected: every package PASS with no race reports.

- [ ] **Step 4: Run the normal suite and static analysis**

Run:

```powershell
$env:GOCACHE=(Join-Path (Get-Location) '.gocache')
go test ./... -count=1
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
go vet ./...
```

Expected: every package PASS and `go vet` exits 0 with no diagnostics.

- [ ] **Step 5: Review the final diff against the design**

Run:

```powershell
git status --short
git diff --stat HEAD
git diff HEAD -- internal/runtime/manager.go internal/runtime/manager_test.go internal/httpapi/response.go internal/httpapi/httpapi_test.go web/js/api.js web/js/devices.js web/js/device.js web/embed_test.go README.md
```

Confirm all of the following before committing:

- Same-device interactive calls use `TryLock` and return `ErrDeviceBusy` without side effects.
- Different devices remain independent.
- `StartEnabled` and `StopAll` still use blocking locks.
- `Snapshot.Operation` is read and written under `entry.mu`.
- HTTP 409 uses `device_busy` and the approved Chinese message.
- Both pages disable all lifecycle buttons and refresh after a busy response.
- No unrelated files or generated `.gocache` contents are staged.

- [ ] **Step 6: Commit documentation and any final test-only adjustment**

Run:

```powershell
git add README.md
git commit -m "docs: describe lifecycle operation conflicts"
```

Expected: commit succeeds. If verification required a test-only correction, stage only that specific test file with `README.md` and describe it in the commit message.

- [ ] **Step 7: Remove the repository-local Go cache**

Resolve `.gocache` under the repository root, verify its absolute path starts with the repository path, then remove only that directory. Confirm `git status --short` shows no generated artifacts.
