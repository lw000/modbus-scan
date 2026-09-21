# Point Batch Delete and Realtime WebSocket Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add current-page point deletion, the revised point table, and a per-point realtime dialog driven by filtered WebSocket subscriptions.

**Architecture:** Extend the point store/service/API with one transactional batch operation. Add a bounded in-memory realtime Hub whose subscriptions are keyed by device ID and tag; collector runners publish value events after UDM updates, while the WebSocket handler supplies the initial runtime snapshot and forwards only matching events. Keep curve samples exclusively in the browser dialog and draw them with Canvas.

**Tech Stack:** Go 1.21, Gin 1.10, SQLite, `github.com/gorilla/websocket` v1.5.3 (server use only), embedded HTML/CSS/vanilla JavaScript, Go `testing` package.

## Global Constraints

- Preserve all existing uncommitted user changes and avoid unrelated formatting or refactors.
- Keep `GET /api/v1/devices/:id/values` compatible.
- Do not change the SQLite schema or persist chart history.
- A batch contains 1-500 unique positive point IDs and is atomic for one device.
- Each WebSocket connection may subscribe to at most 500 `device_id + tag_name` keys.
- Collector publication must never block Modbus scanning.
- Curve history is limited to 300 valid numeric or boolean samples and is discarded when the dialog closes.
- All Go production changes follow a failing-test-first RED-GREEN-REFACTOR cycle and are formatted with `gofmt`.

---

## File Structure

- `internal/store/point.go`: transactional ownership validation and batch point deletion.
- `internal/store/store_test.go`: atomic deletion and configuration-version tests.
- `internal/service/point.go`: service boundary for batch deletion.
- `internal/service/service_test.go`: service delegation and device validation tests.
- `internal/httpapi/point_handler.go`: batch-delete HTTP request validation and response.
- `internal/httpapi/httpapi_test.go`: API contract tests and updated point-service stub.
- `internal/realtime/hub.go`: value event type, subscription key, bounded client queues, and Hub lifecycle.
- `internal/realtime/hub_test.go`: routing, unsubscribe, slow-client, and cleanup tests.
- `internal/runtime/manager.go`: runtime value-event sink wiring and current-value lookup.
- `internal/runtime/manager_test.go`: publisher propagation and snapshot lookup tests.
- `internal/collector/runtime.go`: device-aware collector event publication.
- `internal/collector/collector.go`: publish callback after successful UDM update.
- `internal/collector/collector_test.go`: event timestamp/value publication test.
- `internal/httpapi/websocket.go`: WebSocket upgrade, protocol validation, initial snapshot, and read/write loops.
- `internal/httpapi/websocket_test.go`: end-to-end WebSocket protocol tests.
- `internal/httpapi/server.go`: realtime dependency and `/api/v1/ws` registration.
- `cmd/modbus-scan/main.go`: create one Hub and wire it to runtime and HTTP layers.
- `cmd/modbus-scan/main_test.go`: update constructor wiring assertions if required by existing tests.
- `go.mod`, `go.sum`: add the WebSocket implementation.
- `web/device.html`: revised columns, current-page delete button, realtime dialog, and Canvas.
- `web/js/api.js`: optional JSON request body for DELETE.
- `web/js/device.js`: page-ID tracking, batch delete, dialog subscription, reconnect, and chart drawing.
- `web/css/app.css`: realtime dialog/chart/status styling.
- `web/embed_test.go`: static UI contract assertions.
- `README.md`, `docs/项目设计开发技术文档.md`: document the batch endpoint and WebSocket protocol.

### Task 1: Transactional Point Batch Deletion

**Files:**
- Modify: `internal/store/point.go`
- Modify: `internal/store/store_test.go`
- Modify: `internal/service/point.go`
- Modify: `internal/service/service_test.go`
- Modify: `internal/httpapi/server.go`
- Modify: `internal/httpapi/point_handler.go`
- Modify: `internal/httpapi/httpapi_test.go`

**Interfaces:**
- Produces store method: `DeletePoints(ctx context.Context, deviceID int64, pointIDs []int64) (int, error)`.
- Produces service method: `DeleteBatch(ctx context.Context, deviceID int64, pointIDs []int64) (int, error)`.
- Produces endpoint: `DELETE /api/v1/devices/:id/points` with body `{"ids":[1,2]}`.

- [ ] **Step 1: Write failing store tests for atomic ownership and one version bump**

Add table setup helpers and assertions equivalent to:

```go
func TestDeletePointsIsAtomicAndBumpsVersionOnce(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	first, _ := s.CreateDevice(ctx, testDevice("first"))
	second, _ := s.CreateDevice(ctx, testDevice("second"))
	a, _ := s.CreatePoint(ctx, model.Point{DeviceID: first.ID, TagName: "A", RegType: "HoldingReg", DataType: "UInt16", BitLen: 16})
	b, _ := s.CreatePoint(ctx, model.Point{DeviceID: first.ID, TagName: "B", RegType: "HoldingReg", DataType: "UInt16", BitLen: 16})
	foreign, _ := s.CreatePoint(ctx, model.Point{DeviceID: second.ID, TagName: "C", RegType: "HoldingReg", DataType: "UInt16", BitLen: 16})

	before, _ := s.GetDevice(ctx, first.ID)
	if _, err := s.DeletePoints(ctx, first.ID, []int64{a.ID, foreign.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-device delete error = %v", err)
	}
	if page, _ := s.ListPoints(ctx, first.ID, PointFilter{Limit: 50}); page.Total != 2 {
		t.Fatalf("atomic rollback total = %d", page.Total)
	}
	deleted, err := s.DeletePoints(ctx, first.ID, []int64{a.ID, b.ID})
	if err != nil || deleted != 2 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	after, _ := s.GetDevice(ctx, first.ID)
	if after.ConfigVersion != before.ConfigVersion+1 {
		t.Fatalf("version before=%d after=%d", before.ConfigVersion, after.ConfigVersion)
	}
}
```

- [ ] **Step 2: Run the store test and verify RED**

Run: `go test ./internal/store -run TestDeletePointsIsAtomicAndBumpsVersionOnce -count=1`

Expected: compilation fails because `(*Store).DeletePoints` does not exist.

- [ ] **Step 3: Implement the minimal transactional store method**

Implement `DeletePoints` using one transaction: query `COUNT(*) FROM points WHERE device_id=? AND id IN (...)`, require an exact count match, execute the delete with the same arguments, call `bumpVersion` once, and commit. Generate placeholders only from the already validated slice length; never interpolate IDs into SQL. Return `ErrNotFound` on ownership/count mismatch.

- [ ] **Step 4: Run store tests and verify GREEN**

Run: `go test ./internal/store -count=1`

Expected: PASS.

- [ ] **Step 5: Write failing service and HTTP contract tests**

Extend `PointStore` and `stubPoints`, then add tests for a successful response and invalid payloads:

```go
func TestDeletePointBatch(t *testing.T) {
	points := &stubPoints{deletedCount: 2}
	router := testRouter(&stubDevices{}, points, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/devices/1/points", strings.NewReader(`{"ids":[11,12]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"deleted":2`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
```

Add separate cases for empty IDs, zero/negative IDs, duplicates, and 501 IDs; each must return 400 without calling the service.

- [ ] **Step 6: Run API tests and verify RED**

Run: `go test ./internal/httpapi ./internal/service -run 'TestDeletePointBatch|TestPointServiceDeleteBatch' -count=1`

Expected: compilation or route failure because `DeleteBatch` and the collection DELETE route are absent.

- [ ] **Step 7: Implement service validation and handler**

Add `DeleteBatch` to the service and `PointService` interface. The service checks that the device exists, validates 1-500 unique positive IDs, and calls the store. Register `api.DELETE("/devices/:id/points", h.deletePoints)` before the `/:pointID` route. Decode with `decodeJSON` into:

```go
type deletePointsRequest struct {
	IDs []int64 `json:"ids"`
}
```

Return `gin.H{"deleted": count}` on success and a `ValidationError`-compatible 400 response for invalid IDs.

- [ ] **Step 8: Run focused and package tests**

Run: `gofmt -w internal/store/point.go internal/store/store_test.go internal/service/point.go internal/service/service_test.go internal/httpapi/server.go internal/httpapi/point_handler.go internal/httpapi/httpapi_test.go`

Run: `go test ./internal/store ./internal/service ./internal/httpapi -count=1`

Expected: PASS.

- [ ] **Step 9: Commit the batch-delete slice**

```powershell
git add internal/store/point.go internal/store/store_test.go internal/service/point.go internal/service/service_test.go internal/httpapi/server.go internal/httpapi/point_handler.go internal/httpapi/httpapi_test.go
git commit -m "feat: delete a page of points atomically"
```

### Task 2: Realtime Hub and Runtime Event Boundary

**Files:**
- Create: `internal/realtime/hub.go`
- Create: `internal/realtime/hub_test.go`
- Modify: `internal/runtime/manager.go`
- Modify: `internal/runtime/manager_test.go`

**Interfaces:**
- Produces `realtime.ValueEvent { DeviceID int64; TagName string; Value any; UpdatedAt time.Time }`.
- Produces `Hub.Subscribe(buffer int) *Subscription`, `Subscription.Set(deviceID int64, tags []string) error`, `Subscription.Remove(deviceID int64, tags []string)`, `Subscription.Events() <-chan ValueEvent`, `Subscription.Done() <-chan struct{}`, and `Subscription.Close()`.
- Produces `Hub.Publish(ValueEvent)` as a non-blocking operation.
- Changes runtime constructor to `NewManager(source ConfigSource, factory Factory, publish func(realtime.ValueEvent)) *Manager`.
- Produces `Manager.Value(ctx context.Context, deviceID int64, tag string) (udm.Value, bool, error)` for initial snapshots.

- [ ] **Step 1: Write failing Hub behavior tests**

Test two subscriptions with overlapping tags, unsubscribe, and a one-element slow queue. Assert that only exact device/tag matches arrive and `Publish` returns promptly when a client stops reading. Use timeouts no longer than 250 ms.

```go
func TestHubRoutesOnlyExactSubscriptions(t *testing.T) {
	h := NewHub(500)
	s := h.Subscribe(4)
	defer s.Close()
	if err := s.Set(1, []string{"Speed"}); err != nil { t.Fatal(err) }
	h.Publish(ValueEvent{DeviceID: 1, TagName: "Speed", Value: 12})
	select {
	case got := <-s.Events():
		if got.TagName != "Speed" || got.Value != 12 { t.Fatalf("event=%#v", got) }
	case <-time.After(250 * time.Millisecond):
		t.Fatal("missing matching event")
	}
}
```

- [ ] **Step 2: Run Hub tests and verify RED**

Run: `go test ./internal/realtime -count=1`

Expected: package/types do not exist.

- [ ] **Step 3: Implement the bounded Hub**

Use one mutex around `map[subscriptionKey]map[*Subscription]struct{}` and each subscription's key set. `Publish` copies the target subscriber set under a read lock, then performs non-blocking sends guarded by each subscription's `Done` channel. If a queue is full, call `Close`, which removes all keys and closes `Done` exactly once; do not close the event queue, which avoids a send/close race with a publisher that already copied the subscriber set. Validate device IDs, trimmed non-empty tags, uniqueness, and the configured maximum.

- [ ] **Step 4: Run Hub tests and verify GREEN**

Run: `go test ./internal/realtime -count=1`

Expected: PASS under normal and slow-client cases.

- [ ] **Step 5: Write failing runtime publisher and lookup tests**

Modify `fakeFactory` so its runner can invoke a supplied value sink. Assert that the manager attaches the correct `DeviceID` and forwards the event to the injected publisher. Add a `Value` test that returns the active runner value and reports `ok == false` for an unknown tag.

- [ ] **Step 6: Run runtime tests and verify RED**

Run: `go test ./internal/runtime -run 'TestManagerPublishesRunnerValues|TestManagerValue' -count=1`

Expected: constructor/signature compilation failure.

- [ ] **Step 7: Add value-event plumbing to runtime abstractions**

Change `Factory.New` to accept `ValueSink func(tag string, value udm.Value)`. In `startLocked`, wrap it as a `realtime.ValueEvent` with the immutable device ID and call the manager's optional publisher. Implement `Value` by checking `runner.Values()` or `lastValues` and preserve the existing snapshot locking rules.

- [ ] **Step 8: Format and run focused tests**

Run: `gofmt -w internal/realtime/hub.go internal/realtime/hub_test.go internal/runtime/manager.go internal/runtime/manager_test.go`

Run: `go test ./internal/realtime ./internal/runtime -count=1`

Expected: PASS.

- [ ] **Step 9: Commit the Hub and runtime boundary**

```powershell
git add internal/realtime internal/runtime/manager.go internal/runtime/manager_test.go
git commit -m "feat: add filtered realtime value hub"
```

### Task 3: Publish Successful Collector Updates

**Files:**
- Modify: `internal/collector/collector.go`
- Modify: `internal/collector/collector_test.go`
- Modify: `internal/collector/runtime.go`
- Modify: `internal/collector/runtime_test.go`

**Interfaces:**
- Consumes runtime `ValueSink func(tag string, value udm.Value)`.
- Changes collector constructor to `NewCollector(connMgr, udm, chunks, cfg, publish func(string, udm.Value)) *Collector`.

- [ ] **Step 1: Write a failing test for UDM update plus publication**

Extract the already decoded update into a focused method whose desired behavior is explicit:

```go
func TestCollectorUpdateValuePublishesSameTimestamp(t *testing.T) {
	var tag string
	var event udm.Value
	u := udm.New()
	c := &Collector{udm: u, publish: func(gotTag string, got udm.Value) { tag, event = gotTag, got }}
	c.updateValue("Speed", float64(42))
	snapshot := u.Snapshot()["Speed"]
	if tag != "Speed" || event.Value != float64(42) || !event.UpdatedAt.Equal(snapshot.UpdatedAt) {
		t.Fatalf("tag=%q event=%#v snapshot=%#v", tag, event, snapshot)
	}
}
```

- [ ] **Step 2: Run the collector test and verify RED**

Run: `go test ./internal/collector -run TestCollectorUpdateValuePublishesSameTimestamp -count=1`

Expected: compilation fails because `publish` and `updateValue` are absent.

- [ ] **Step 3: Implement one timestamped update path**

Implement `updateValue` with `at := time.Now().UTC()`, `udm.UpdateAt(tag, value, at)`, then invoke the optional callback with `udm.Value{Value: value, UpdatedAt: at}`. Replace the direct `c.udm.Update` at the end of point decoding with this method. Pass the runtime factory's `ValueSink` to `NewCollector`.

- [ ] **Step 4: Update factory tests and run GREEN**

Update every `RuntimeFactory.New` call for the new value sink parameter, format, then run:

`go test ./internal/collector ./internal/runtime -count=1`

Expected: PASS.

- [ ] **Step 5: Commit collector publication**

```powershell
git add internal/collector/collector.go internal/collector/collector_test.go internal/collector/runtime.go internal/collector/runtime_test.go
git commit -m "feat: publish collected point values"
```

### Task 4: WebSocket Protocol and Server Wiring

**Files:**
- Create: `internal/httpapi/websocket.go`
- Create: `internal/httpapi/websocket_test.go`
- Modify: `internal/httpapi/server.go`
- Modify: `internal/httpapi/httpapi_test.go`
- Modify: `cmd/modbus-scan/main.go`
- Modify: `cmd/modbus-scan/main_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Consumes `*realtime.Hub` and a `RealtimeValues` interface with `Value(context.Context, int64, string) (udm.Value, bool, error)`.
- Changes router constructor to `NewRouter(logger, devices, points, realtimeValues, hub, webFS)`.
- Produces `/api/v1/ws` messages defined in the approved specification.

- [ ] **Step 1: Add dependency and write failing WebSocket tests**

Run: `go get github.com/gorilla/websocket@v1.5.3`

Create an `httptest.Server`, dial `/api/v1/ws`, send a subscribe message, and assert the initial snapshot arrives. Publish a matching and a nonmatching event and assert only the matching event arrives. Add malformed JSON, invalid ID, empty tags, more than 500 tags, unsubscribe, and connection-close cleanup cases.

```go
type realtimeStub struct{ values map[int64]map[string]udm.Value }

func (s realtimeStub) Value(_ context.Context, deviceID int64, tag string) (udm.Value, bool, error) {
	value, ok := s.values[deviceID][tag]
	return value, ok, nil
}
```

- [ ] **Step 2: Run WebSocket tests and verify RED**

Run: `go test ./internal/httpapi -run TestWebSocket -count=1`

Expected: route or handler symbols are missing.

- [ ] **Step 3: Implement the handler and protocol loops**

Use `websocket.Upgrader.CheckOrigin` to accept requests with no Origin (non-browser clients) or the same host only. Set a 16 KiB read limit, 30-second write deadline, 60-second pong deadline, and periodic ping. Decode:

```go
type wsCommand struct {
	Action   string   `json:"action"`
	DeviceID int64    `json:"device_id"`
	Tags     []string `json:"tags"`
}

type wsMessage struct {
	Type      string    `json:"type"`
	Code      string    `json:"code,omitempty"`
	Message   string    `json:"message,omitempty"`
	DeviceID  int64     `json:"device_id,omitempty"`
	TagName   string    `json:"tag_name,omitempty"`
	Value     any       `json:"value,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}
```

One goroutine reads commands and mutates the subscription; one goroutine is the sole writer for Hub events, protocol errors, snapshot values, and pings. Snapshot messages enter that writer through a bounded local channel. Close the subscription when either loop exits.

- [ ] **Step 4: Wire the Hub into router and main**

Construct `hub := realtime.NewHub(500)`, pass `hub.Publish` to `runtime.NewManager`, and pass manager plus Hub into `httpapi.NewRouter`. Update test constructors with `nil` realtime dependencies when WebSocket is not under test. Ensure server shutdown causes request contexts and connection loops to exit.

- [ ] **Step 5: Run focused tests and verify GREEN**

Run: `gofmt -w internal/httpapi/websocket.go internal/httpapi/websocket_test.go internal/httpapi/server.go internal/httpapi/httpapi_test.go cmd/modbus-scan/main.go cmd/modbus-scan/main_test.go`

Run: `go test ./internal/httpapi ./cmd/modbus-scan -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the WebSocket server**

```powershell
git add go.mod go.sum internal/httpapi cmd/modbus-scan
git commit -m "feat: serve subscribed realtime values over websocket"
```

### Task 5: Point Table, Current-Page Delete, and Realtime Dialog

**Files:**
- Modify: `web/device.html`
- Modify: `web/js/api.js`
- Modify: `web/js/device.js`
- Modify: `web/css/app.css`
- Modify: `web/embed_test.go`

**Interfaces:**
- Consumes batch endpoint and `/api/v1/ws` protocol.
- Produces UI IDs `delete-page`, `realtime-dialog`, `realtime-tag`, `realtime-value`, `realtime-updated`, `realtime-status`, and `realtime-chart`.

- [ ] **Step 1: Write failing embedded-asset contract tests**

Extend `web/embed_test.go` to assert the exact `<colgroup>` order, the “读写” heading, delete-page button, realtime dialog, Canvas, and required JavaScript behavior markers:

```go
for _, required := range []string{
	`id="delete-page"`, `id="realtime-dialog"`, `id="realtime-chart"`,
	`data-column="writeable"`, `data-column="description"`, `>读写<`,
} {
	if !strings.Contains(page, required) { t.Errorf("device.html missing %q", required) }
}
for _, required := range []string{"openRealtime", "subscribe", "unsubscribe", "MAX_CHART_SAMPLES", "pageItems.map"} {
	if !strings.Contains(script, required) { t.Errorf("device.js missing %q", required) }
}
```

Also compare string indexes to prove `writeable` appears before `description` and description appears after it.

- [ ] **Step 2: Run web tests and verify RED**

Run: `go test ./web -count=1`

Expected: missing markup and script markers.

- [ ] **Step 3: Implement the revised table and batch-delete state**

Move the columns to the approved order, rename the heading, render `point.writeable === 1 ? "读写" : "只读"`, and add `pageItems` beside `pageState`. Assign it only from the latest successful `loadPoints` response. The delete handler sends `api.delete(path, {ids: pageItems.map(point => point.id)})`, disables the button during the request, confirms the count, and calls `loadPoints`; update `api.delete` to accept an optional JSON body without breaking existing calls.

- [ ] **Step 4: Implement realtime dialog lifecycle and chart**

Add a “实时值” row button carrying the escaped point JSON. `openRealtime(point)` resets arrays and labels, opens the dialog, and creates `new WebSocket(`${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/api/v1/ws`)`. On open send one subscribe command. On value messages for the active point, update text/time and append numeric or boolean samples, cap with `samples.splice(0, samples.length - MAX_CHART_SAMPLES)`, then redraw Canvas with device-pixel-ratio scaling and an empty/flat-series-safe Y range. On close button or dialog close event, send unsubscribe when open, close the socket, cancel the reconnect timer, and clear samples.

Use reconnect delays `[500, 1000, 2000, 5000]` milliseconds capped at 5 seconds. Guard every callback with a monotonically increasing dialog session ID so stale sockets cannot update a newly opened point.

- [ ] **Step 5: Add focused CSS and run GREEN**

Add a responsive dialog width, chart border/background, status colors, and a minimum Canvas height without changing unrelated styles.

Run: `go test ./web -count=1`

Expected: PASS.

- [ ] **Step 6: Manually smoke-test browser behavior**

Start the service against a test configuration, then verify: filtered page deletion removes only visible IDs; zero-row button is disabled; a numeric point draws; a Bool point steps between 0/1; closing/reopening clears the chart; stopping and restarting the server changes status and reconnects; two tabs subscribed to different tags do not receive each other's values.

- [ ] **Step 7: Commit the UI slice**

```powershell
git add web/device.html web/js/api.js web/js/device.js web/css/app.css web/embed_test.go
git commit -m "feat: add point realtime chart and page deletion"
```

### Task 6: Documentation and Full Verification

**Files:**
- Modify: `README.md`
- Modify: `docs/项目设计开发技术文档.md`

**Interfaces:**
- Documents `DELETE /api/v1/devices/:id/points` and `GET /api/v1/ws`.

- [ ] **Step 1: Update user and protocol documentation**

Document the request/response JSON, atomic ownership semantics, 500-ID limits, subscribe/unsubscribe commands, value/error messages, same-origin requirement, nonpersistent 300-sample browser curve, and the fact that configuration changes require the existing device restart flow.

- [ ] **Step 2: Run formatting and dependency checks**

Run: `gofmt -w cmd internal`

Run: `go mod tidy`

Run: `git diff --check`

Expected: no formatting errors or unexpected dependency changes beyond the chosen WebSocket module.

- [ ] **Step 3: Run the full unit and static test suites**

Run: `go test ./... -count=1`

Run: `go vet ./...`

Expected: both commands exit 0.

- [ ] **Step 4: Run race detection**

Run: `go test -race ./... -count=1`

Expected: exit 0 with no race reports. If the Windows environment lacks a race-enabled CGO toolchain, record the exact error and run `go test -race ./internal/realtime ./internal/runtime ./internal/httpapi -count=1` on a supported Go toolchain before release.

- [ ] **Step 5: Review the final diff against the specification**

Confirm all four requested UI behaviors, filtered subscription routing, initial snapshot, shutdown cleanup, and compatibility of `/values`. Confirm generated binaries, logs, databases, caches, and local configuration are not staged.

- [ ] **Step 6: Commit documentation and verification-ready state**

```powershell
git add README.md docs/项目设计开发技术文档.md
git commit -m "docs: describe realtime point subscriptions"
```
