# Kafka Realtime Publishing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add dynamically configurable, per-device Kafka publishing with mutually exclusive full and change modes while keeping Modbus collection and WebSocket delivery non-blocking.

**Architecture:** SQLite stores one global Kafka connection record and one publishing record per device. Each completed scan submits an immutable snapshot to a coordinator, which applies per-device full/change rules and places messages on a bounded in-memory queue consumed by a Sarama-backed lifecycle manager. HTTP/WebSocket and Kafka infrastructure start before device collectors; shutdown proceeds in reverse data-flow order.

**Tech Stack:** Go 1.21, SQLite (`modernc.org/sqlite`), Gin, IBM Sarama, standard-library JSON/TLS, embedded HTML/CSS/JavaScript.

## Global Constraints

- Kafka connection and per-device publishing configuration must be persisted in SQLite and update without restarting the process or device collector.
- Per-device mode is exactly one of `full` or `change`; both modes cannot be active together.
- Kafka network I/O must never block Modbus scan execution.
- The bounded queue drops its oldest item on overflow and logs a rate-limited warning; messages are not persisted or replayed after restart.
- Kafka and HTTP/WebSocket infrastructure must accept events before enabled device collectors start.
- Existing WebSocket protocol, device APIs, point CSV format, and default behavior remain backward compatible.
- Kafka is globally disabled and every device publisher is disabled after migration.
- Never return or log the saved SASL password, private key contents, or Kafka message body.

## File Structure

- Create `internal/model/kafka.go`: persisted settings, device config, status, and wire-message types.
- Modify `internal/store/store.go`: schema migrations 3 and 4.
- Modify `internal/store/device.go`: transactional default device Kafka config creation.
- Create `internal/store/kafka.go`: global and device Kafka configuration persistence.
- Modify `internal/store/store_test.go`: migration, default, update, and cascade coverage.
- Create `internal/kafkapub/config.go`: validation and Sarama-independent normalized connection options.
- Create `internal/kafkapub/coordinator.go`: scan snapshots, full/change state, bounded oldest-drop queue.
- Create `internal/kafkapub/coordinator_test.go`: deterministic coordinator tests.
- Create `internal/kafkapub/producer.go`: producer interface and Sarama adapter.
- Create `internal/kafkapub/manager.go`: dynamic connection lifecycle, status, reconnect, and drain.
- Create `internal/kafkapub/manager_test.go`: fake-producer lifecycle tests.
- Modify `internal/collector/collector.go`: publish one completed scan notification.
- Modify `internal/collector/collector_test.go`: completed and failed scan notification tests.
- Modify `internal/collector/runtime.go`: attach device identity and submit immutable snapshots.
- Modify `internal/collector/runtime_test.go`: snapshot bridge coverage.
- Create `internal/service/kafka.go`: global/device Kafka use cases and password-preservation semantics.
- Create `internal/service/kafka_test.go`: validation and hot-reload service tests.
- Modify `internal/httpapi/server.go`: Kafka service boundary and route registration.
- Create `internal/httpapi/kafka_handler.go`: global and device Kafka endpoints.
- Modify `internal/httpapi/httpapi_test.go`: endpoint, redaction, and validation tests.
- Modify `cmd/modbus-scan/main.go`: dependency assembly, startup ordering, and reverse shutdown.
- Modify `cmd/modbus-scan/main_test.go`: lifecycle ordering test.
- Modify `web/index.html`, `web/js/devices.js`: global Kafka dialog and status.
- Modify `web/device.html`, `web/js/device.js`: per-device Kafka controls.
- Modify `web/css/app.css`, `web/embed_test.go`: presentation and embedded asset tests.
- Modify `README.md`: configuration, contract, failure behavior, and integration instructions.
- Modify `go.mod`, `go.sum`: pinned IBM Sarama dependency.

---

### Task 1: Persist Global and Per-Device Kafka Configuration

**Files:**
- Create: `internal/model/kafka.go`
- Create: `internal/store/kafka.go`
- Modify: `internal/store/store.go`
- Modify: `internal/store/device.go`
- Modify: `internal/store/store_test.go`

**Interfaces:**
- Produces: `model.KafkaSettings`, `model.DeviceKafkaConfig`, `Store.GetKafkaSettings`, `Store.UpdateKafkaSettings`, `Store.GetDeviceKafkaConfig`, and `Store.UpdateDeviceKafkaConfig`.
- Consumes: existing `Store`, `mapError`, and `requireAffected` helpers.

- [ ] **Step 1: Write failing schema and CRUD tests**

Add tests that create a device, verify its default Kafka row, update that row without changing `devices.config_version`, delete the device and verify cascade deletion. Add a legacy database migration test that starts at migration 2 and verifies singleton row `id=1` and one default row for every existing device.

```go
func TestDeviceKafkaConfigDefaultsUpdatesAndCascades(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	device, err := s.CreateDevice(ctx, testDevice("plc"))
	if err != nil { t.Fatal(err) }
	before := device.ConfigVersion
	cfg, err := s.GetDeviceKafkaConfig(ctx, device.ID)
	if err != nil { t.Fatal(err) }
	if cfg.Enabled || cfg.Mode != model.KafkaModeChange || cfg.FullIntervalSec != 60 { t.Fatalf("default = %#v", cfg) }
	cfg.Enabled, cfg.Topic, cfg.Mode, cfg.FullIntervalSec = true, "plc.values", model.KafkaModeFull, 30
	if _, err := s.UpdateDeviceKafkaConfig(ctx, cfg); err != nil { t.Fatal(err) }
	after, _ := s.GetDevice(ctx, device.ID)
	if after.ConfigVersion != before { t.Fatalf("config version changed: %d -> %d", before, after.ConfigVersion) }
	if err := s.DeleteDevice(ctx, device.ID); err != nil { t.Fatal(err) }
	if _, err := s.GetDeviceKafkaConfig(ctx, device.ID); !errors.Is(err, ErrNotFound) { t.Fatalf("error = %v", err) }
}
```

- [ ] **Step 2: Run the focused tests and verify failure**

Run: `go test ./internal/store -run 'Test(DeviceKafka|OpenMigratesKafka)' -count=1`

Expected: FAIL because Kafka model types and store methods do not exist.

- [ ] **Step 3: Add model types and exact migrations**

Define constants and types:

```go
const (
	KafkaModeFull   = "full"
	KafkaModeChange = "change"
)

type KafkaSettings struct {
	Enabled bool `json:"enabled"`
	Brokers []string `json:"brokers"`
	ClientID string `json:"client_id"`
	KafkaVersion string `json:"kafka_version"`
	SecurityProtocol string `json:"security_protocol"`
	SASLMechanism string `json:"sasl_mechanism"`
	SASLUsername string `json:"sasl_username"`
	SASLPassword string `json:"-"`
	SSLCAPath string `json:"ssl_ca_location"`
	SSLCertificatePath string `json:"ssl_certificate_location"`
	SSLKeyPath string `json:"ssl_key_location"`
	SSLEndpointIdentificationAlgorithm string `json:"ssl_endpoint_identification_algorithm"`
	QueueCapacity int `json:"queue_capacity"`
	UpdatedAt time.Time `json:"updated_at"`
}

type DeviceKafkaConfig struct {
	DeviceID int64 `json:"device_id"`
	Enabled bool `json:"enabled"`
	Topic string `json:"topic"`
	Mode string `json:"mode"`
	FullIntervalSec int `json:"full_interval_sec"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type KafkaStatus struct {
	State string `json:"state"`
	LastError string `json:"last_error,omitempty"`
	DroppedMessages uint64 `json:"dropped_messages"`
}
```

Migration 3 creates `kafka_settings` and inserts the default singleton. Migration 4 creates `device_kafka_configs`, inserts defaults for existing devices, and records both migrations transactionally. Update `CreateDevice` to insert its default Kafka row in the same transaction as the device row.

- [ ] **Step 4: Implement store reads and updates**

Use JSON encoding for `brokers`, integer conversion for booleans, parameterized SQL, `rows.Err()` where applicable, and wrap errors with operation context. `UpdateDeviceKafkaConfig` must first ensure the device exists and must not update `devices.config_version`.

- [ ] **Step 5: Run store tests**

Run: `go test ./internal/store -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the storage slice**

```powershell
git add internal/model/kafka.go internal/store/store.go internal/store/device.go internal/store/kafka.go internal/store/store_test.go
git commit -m "feat: persist Kafka publishing configuration"
```

### Task 2: Validate and Apply Kafka Configuration Through Services

**Files:**
- Create: `internal/kafkapub/config.go`
- Create: `internal/kafkapub/config_test.go`
- Create: `internal/service/kafka.go`
- Create: `internal/service/kafka_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Consumes: Task 1 store methods and model types.
- Produces: `kafkapub.ValidateSettings`, `kafkapub.ValidateDeviceConfig`, `service.KafkaService`, and the `service.KafkaRuntime` hot-reload boundary.

- [ ] **Step 1: Write table-driven validation tests**

Cover enabled settings with no broker, malformed broker, invalid Kafka version, inconsistent SASL settings, unpaired client certificate/key, invalid queue capacity, invalid Topic, invalid mode, and interval values 0 and 86401. Verify disabled global settings accept an empty broker list.

```go
func TestValidateDeviceConfig(t *testing.T) {
	tests := []struct{name string; cfg model.DeviceKafkaConfig; wantErr bool}{
		{"valid change", model.DeviceKafkaConfig{Enabled:true, Topic:"plc.values", Mode:model.KafkaModeChange, FullIntervalSec:60}, false},
		{"both modes cannot be encoded", model.DeviceKafkaConfig{Enabled:true, Topic:"plc", Mode:"full,change", FullIntervalSec:60}, true},
		{"invalid topic", model.DeviceKafkaConfig{Enabled:true, Topic:"bad topic", Mode:model.KafkaModeFull, FullIntervalSec:60}, true},
		{"invalid interval", model.DeviceKafkaConfig{Mode:model.KafkaModeFull, FullIntervalSec:0}, true},
	}
	for _, tt := range tests { t.Run(tt.name, func(t *testing.T) { if (ValidateDeviceConfig(tt.cfg) != nil) != tt.wantErr { t.Fatalf("unexpected result") } }) }
}
```

Add the pinned dependency before compiling the tests:

Run: `go get github.com/IBM/sarama@v1.42.1`

- [ ] **Step 2: Run validation tests and verify failure**

Run: `go test ./internal/kafkapub ./internal/service -run 'TestValidate|TestKafkaService' -count=1`

Expected: FAIL because the packages and service do not exist.

- [ ] **Step 3: Implement normalization and validation**

Normalize whitespace and uppercase protocol/mechanism fields. Parse brokers with `net.SplitHostPort`, Kafka version with `sarama.ParseKafkaVersion`, and resolve TLS paths against the service config directory before file access. Validate Topic with `^[A-Za-z0-9._-]+$`, reject `.` and `..`, and enforce all numeric bounds from the design.

- [ ] **Step 4: Implement the service boundary**

Define:

```go
type KafkaStore interface {
	GetKafkaSettings(context.Context) (model.KafkaSettings, error)
	UpdateKafkaSettings(context.Context, model.KafkaSettings) (model.KafkaSettings, error)
	GetDeviceKafkaConfig(context.Context, int64) (model.DeviceKafkaConfig, error)
	UpdateDeviceKafkaConfig(context.Context, model.DeviceKafkaConfig) (model.DeviceKafkaConfig, error)
}

type KafkaRuntime interface {
	ApplySettings(context.Context, model.KafkaSettings) (model.KafkaStatus, error)
	ApplyDeviceConfig(model.DeviceKafkaConfig)
	Status() model.KafkaStatus
}
```

`UpdateSettings` preserves the stored password when the request password is empty, clears it only when `clear_sasl_password` is true, calls `ApplySettings` before persistence, and persists only after a successful runtime switch. `UpdateDeviceConfig` validates, persists, then calls `ApplyDeviceConfig`.

- [ ] **Step 5: Run service tests**

Run: `go test ./internal/kafkapub ./internal/service -count=1`

Expected: PASS for the configuration and service tests; lifecycle fakes may remain minimal until Task 4.

- [ ] **Step 6: Commit the validation slice**

```powershell
git add go.mod go.sum internal/kafkapub/config.go internal/kafkapub/config_test.go internal/service/kafka.go internal/service/kafka_test.go
git commit -m "feat: validate dynamic Kafka configuration"
```

### Task 3: Build Deterministic Full/Change Coordination and the Bounded Queue

**Files:**
- Create: `internal/kafkapub/coordinator.go`
- Create: `internal/kafkapub/coordinator_test.go`

**Interfaces:**
- Consumes: `model.DeviceKafkaConfig` from Task 1.
- Produces: `kafkapub.ScanSnapshot`, `kafkapub.Message`, `kafkapub.Coordinator.Submit`, `Messages`, `ApplyDeviceConfig`, `ResetDevice`, `Start`, and `StopAccepting`.

- [ ] **Step 1: Write failing coordinator tests with a fake clock**

Test change-mode baseline/no first message, changed-only payload, failed-point baseline preservation, full-mode immediate message, full interval suppression/release, configuration reset, disabled filtering, and oldest-drop behavior.

```go
func TestChangeModeBuildsBaselineThenPublishesOnlyChanges(t *testing.T) {
	now := time.Date(2026, 9, 24, 2, 0, 0, 0, time.UTC)
	c := newTestCoordinator(4, func() time.Time { return now })
	c.ApplyDeviceConfig(model.DeviceKafkaConfig{DeviceID:1, Enabled:true, Topic:"plc", Mode:model.KafkaModeChange, FullIntervalSec:60})
	c.Submit(ScanSnapshot{DeviceID:1, DeviceName:"PLC", CollectedAt:now, SuccessfulValues:map[string]any{"A":1.0,"B":true}, CurrentValues:map[string]any{"A":1.0,"B":true}})
	assertNoMessage(t, c.Messages())
	now = now.Add(time.Second)
	c.Submit(ScanSnapshot{DeviceID:1, DeviceName:"PLC", CollectedAt:now, SuccessfulValues:map[string]any{"A":2.0,"B":true}, CurrentValues:map[string]any{"A":2.0,"B":true}})
	got := receiveMessage(t, c.Messages())
	if got.Type != model.KafkaModeChange || !reflect.DeepEqual(got.Points, map[string]any{"A":2.0}) { t.Fatalf("message = %#v", got) }
}
```

- [ ] **Step 2: Run coordinator tests and verify failure**

Run: `go test ./internal/kafkapub -run 'Test(Change|Full|Queue|DeviceConfig)' -count=1`

Expected: FAIL because coordinator types do not exist.

- [ ] **Step 3: Implement immutable snapshots and wire messages**

```go
type ScanSnapshot struct {
	DeviceID int64
	DeviceName string
	CollectedAt time.Time
	SuccessfulValues map[string]any
	CurrentValues map[string]any
}

type Message struct {
	SchemaVersion int `json:"schema_version"`
	Type string `json:"message_type"`
	Device DeviceIdentity `json:"device"`
	CollectedAt time.Time `json:"collected_at"`
	Points map[string]any `json:"points"`
	Topic string `json:"-"`
}

type DeviceIdentity struct {
	ID int64 `json:"id"`
	Name string `json:"name"`
}
```

Copy both maps at every ownership boundary. Maintain `lastValues` and `lastFullQueuedAt` per device under one mutex. For a full message, use `CurrentValues`, supplied from the runtime's complete UDM snapshot. For change mode, compare only keys present in `SuccessfulValues` and update only those baselines.

- [ ] **Step 4: Implement an oldest-drop bounded queue**

Use a mutex-protected ring buffer plus a one-element wake channel so `Submit` never waits for the sender. When full, overwrite the head, increment an atomic dropped counter, and invoke a warning callback no more than once per configured rate-limit interval.

- [ ] **Step 5: Run coordinator tests and race tests**

Run: `go test ./internal/kafkapub -run 'Test(Change|Full|Queue|DeviceConfig)' -race -count=1`

Expected: PASS with no race report.

- [ ] **Step 6: Commit the coordinator slice**

```powershell
git add internal/kafkapub/coordinator.go internal/kafkapub/coordinator_test.go
git commit -m "feat: coordinate full and change Kafka messages"
```

### Task 4: Add Sarama Producer and Dynamic Lifecycle Management

**Files:**
- Create: `internal/kafkapub/producer.go`
- Create: `internal/kafkapub/manager.go`
- Create: `internal/kafkapub/manager_test.go`

**Interfaces:**
- Consumes: Task 2 normalized settings and Task 3 coordinator messages.
- Produces: concrete `Manager` implementing `service.KafkaRuntime` and a `ProducerFactory` seam for tests.

```go
type ProducerMessage struct {
	Topic string
	Key string
	Value []byte
}

type Producer interface {
	Send(context.Context, ProducerMessage) error
	Close() error
}

type ProducerFactory interface {
	Open(context.Context, model.KafkaSettings) (Producer, error)
}

func (m *Manager) Start(context.Context, model.KafkaSettings) error
func (m *Manager) ApplySettings(context.Context, model.KafkaSettings) (model.KafkaStatus, error)
func (m *Manager) ApplyDeviceConfig(model.DeviceKafkaConfig)
func (m *Manager) Status() model.KafkaStatus
func (m *Manager) Stop(context.Context) error
```

- [ ] **Step 1: Write failing fake-producer lifecycle tests**

Test startup-disabled, startup-enabled, enabled startup connection failure entering `reconnecting`, candidate replacement success, candidate failure retaining old producer, dynamic disable/drain, JSON/key encoding, and shutdown timeout using the pinned Sarama dependency from Task 2.

```go
type fakeProducer struct { sent chan ProducerMessage; closed atomic.Bool }
func (p *fakeProducer) Send(ctx context.Context, m ProducerMessage) error { select { case p.sent <- m: return nil; case <-ctx.Done(): return ctx.Err() } }
func (p *fakeProducer) Close() error { p.closed.Store(true); return nil }
```

- [ ] **Step 2: Run manager tests and verify failure**

Run: `go test ./internal/kafkapub -run 'TestManager' -count=1`

Expected: FAIL because producer and manager implementations do not exist.

- [ ] **Step 3: Implement the Sarama adapter**

Configure `Producer.RequiredAcks = sarama.WaitForLocal`, `Producer.Return.Successes = true`, `Producer.Return.Errors = true`, finite retry limits, hash partitioning using decimal device ID as the key, SASL PLAIN/SCRAM, TLS root CAs, optional client certificate, and endpoint identification behavior. Wrap every construction and close error with context.

- [ ] **Step 4: Implement lifecycle state and reconnect**

Expose status values `disabled`, `connecting`, `online`, `reconnecting`, `stopping`, and `error`. `Start` begins the queue worker before attempting a connection. Startup connection failure schedules bounded exponential backoff while leaving the queue available. Runtime `ApplySettings` creates and probes a candidate producer before persisting/switching; failure leaves the old producer untouched. Disabling stops admission, drains to the supplied context deadline, closes the producer, and reports `disabled`.

- [ ] **Step 5: Run lifecycle and race tests**

Run: `go test ./internal/kafkapub -race -count=1`

Expected: PASS with no goroutine leaks or race report.

- [ ] **Step 6: Commit the producer slice**

```powershell
git add internal/kafkapub/producer.go internal/kafkapub/manager.go internal/kafkapub/manager_test.go
git commit -m "feat: manage asynchronous Kafka producer lifecycle"
```

### Task 5: Emit Completed Scans and Enforce Startup/Shutdown Ordering

**Files:**
- Modify: `internal/collector/collector.go`
- Modify: `internal/collector/collector_test.go`
- Modify: `internal/collector/runtime.go`
- Modify: `internal/collector/runtime_test.go`
- Modify: `cmd/modbus-scan/main.go`
- Modify: `cmd/modbus-scan/main_test.go`

**Interfaces:**
- Consumes: `kafkapub.Coordinator.Submit`, existing realtime `Hub.Publish`, and Task 4 `Manager`.
- Produces: a scan completion callback carrying successful values, while preserving the existing point callback.

- [ ] **Step 1: Write failing scan completion tests**

Verify one callback after a completely successful scan, no callback when no client exists, and no callback after a communication error. Verify the runtime converts the callback into a snapshot containing device identity, completion time, successful values, and a detached complete UDM snapshot for full mode.

```go
func TestScanOncePublishesOneCompletedScan(t *testing.T) {
	var scans []map[string]any
	c := collectorWithFakeClient(t, func(values map[string]any) { scans = append(scans, values) })
	c.scanOnce()
	if len(scans) != 1 || len(scans[0]) == 0 { t.Fatalf("scans = %#v", scans) }
}
```

- [ ] **Step 2: Run collector tests and verify failure**

Run: `go test ./internal/collector -run 'Test(ScanOncePublishes|RuntimePublishesSnapshot)' -count=1`

Expected: FAIL because scan completion callbacks do not exist.

- [ ] **Step 3: Add the scan completion callback**

Track values successfully parsed during one `scanOnce` call without removing the current per-point `publish` callback. Invoke `scanComplete` exactly once only after all chunks finish successfully. Pass copied maps and a single UTC completion timestamp.

- [ ] **Step 4: Bridge runtime snapshots to the coordinator**

Extend `RuntimeFactory` with a scan publisher interface rather than importing the concrete manager. The runtime combines successful values with `r.values.Snapshot()` and device identity, then submits without waiting. Call `ResetDevice(device.ID)` when the runner exits.

- [ ] **Step 5: Reorder process assembly**

In `runWithReady`, perform this exact sequence: open SQLite; create realtime Hub; create Kafka Coordinator/Manager and services; construct router/server/listener; start `server.Serve`; load/start Kafka runtime; start enabled device collectors; call `ready`. On exit: stop devices; stop Kafka admission and drain/close within shutdown context; shut down HTTP; close SQLite via deferred cleanup. Join all shutdown errors.

- [ ] **Step 6: Run collector and main lifecycle tests**

Run: `go test ./internal/collector ./cmd/modbus-scan -race -count=1`

Expected: PASS, including an ordering fake that rejects collector submission before Kafka runtime start.

- [ ] **Step 7: Commit the integration slice**

```powershell
git add internal/collector/collector.go internal/collector/collector_test.go internal/collector/runtime.go internal/collector/runtime_test.go cmd/modbus-scan/main.go cmd/modbus-scan/main_test.go
git commit -m "feat: publish completed scans after Kafka startup"
```

### Task 6: Expose Global and Device Kafka APIs

**Files:**
- Create: `internal/httpapi/kafka_handler.go`
- Modify: `internal/httpapi/server.go`
- Modify: `internal/httpapi/httpapi_test.go`

**Interfaces:**
- Consumes: `service.KafkaService` from Task 2.
- Produces: `GET/PUT /api/v1/kafka` and `GET/PUT /api/v1/devices/:id/kafka`.

- [ ] **Step 1: Write failing HTTP tests**

Test successful reads/updates, invalid JSON, unknown fields, validation errors, missing device, runtime switch failure, password omission, empty-password preservation, and explicit password clearing.

```go
func TestGetKafkaNeverReturnsPassword(t *testing.T) {
	router := newTestRouterWithKafka(t, &stubKafka{settings:model.KafkaSettings{Enabled:true, SASLPassword:"secret"}})
	response := performRequest(router, http.MethodGet, "/api/v1/kafka", nil)
	if response.Code != http.StatusOK { t.Fatalf("status = %d", response.Code) }
	if strings.Contains(response.Body.String(), "secret") || strings.Contains(response.Body.String(), "sasl_password") { t.Fatalf("body leaks password: %s", response.Body.String()) }
}
```

- [ ] **Step 2: Run HTTP tests and verify failure**

Run: `go test ./internal/httpapi -run 'Test(Get|Put).*Kafka' -count=1`

Expected: FAIL because Kafka routes are not registered.

- [ ] **Step 3: Add service interface and handlers**

Add a required Kafka service parameter to `NewRouter` and update every production and test caller in this task. Reuse `decodeJSON`, `parseID`, `success`, and `handleError`. Define a request DTO with `sasl_password` and `clear_sasl_password`; never serialize the stored password.

- [ ] **Step 4: Run all HTTP tests**

Run: `go test ./internal/httpapi -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the API slice**

```powershell
git add internal/httpapi/server.go internal/httpapi/kafka_handler.go internal/httpapi/httpapi_test.go
git commit -m "feat: expose dynamic Kafka configuration APIs"
```

### Task 7: Add Management UI, Documentation, and Full Verification

**Files:**
- Modify: `web/index.html`
- Modify: `web/js/devices.js`
- Modify: `web/device.html`
- Modify: `web/js/device.js`
- Modify: `web/css/app.css`
- Modify: `web/embed_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: Task 6 HTTP endpoints.
- Produces: global Kafka settings/status UI and per-device publishing UI.

- [ ] **Step 1: Write failing embedded asset assertions**

Assert that the embedded index includes the global Kafka control and that the device page includes Topic, mutually exclusive mode radio controls, full interval, and a separate save action. Assert JavaScript contains the four API endpoint patterns and no password value is populated from a GET response.

- [ ] **Step 2: Run web tests and verify failure**

Run: `go test ./web -count=1`

Expected: FAIL because Kafka controls are absent.

- [ ] **Step 3: Implement global Kafka UI**

Add a dialog reachable from the device-list header. Render runtime state and last error. Use one broker per line in the UI and convert to/from the JSON array. Leave the password input empty on load; an empty value preserves the password. Add an explicit checkbox for clearing the saved password. Disable irrelevant SASL/TLS fields based on protocol without deleting their entered values.

- [ ] **Step 4: Implement per-device Kafka UI**

Add a dedicated card on `device.html`. Load `/api/v1/devices/:id/kafka`, save with PUT, use radio buttons named `kafka-mode` so `full` and `change` cannot both be selected, disable the interval input in change mode, and show validation failures in the existing notice.

- [ ] **Step 5: Update documentation**

Document the two SQLite configuration layers, dynamic behavior, startup and shutdown order, exact JSON schema, full/change semantics, queue overflow behavior, security warning for the unauthenticated local UI, and a copyable Kafka integration procedure using a test Topic and consumer.

- [ ] **Step 6: Format and run the complete verification suite**

Run:

```powershell
Get-ChildItem internal,cmd -Recurse -Filter *.go | ForEach-Object { gofmt -w $_.FullName }
go test ./...
go test -race ./...
go vet ./...
go build -o tmp-modbus-scan.exe ./cmd/modbus-scan
```

Expected: every command exits 0. Remove only the explicitly generated `tmp-modbus-scan.exe` after confirming its resolved path is inside the repository.

- [ ] **Step 7: Review the final diff for secrets and unrelated edits**

Run:

```powershell
git diff --check
git status --short
rg -n "sasl_password|SASLPassword|private key|BEGIN .*PRIVATE KEY" --glob '!go.sum'
```

Expected: no committed credential value, no whitespace errors, and only task-related files changed.

- [ ] **Step 8: Commit the UI and documentation slice**

```powershell
git add web README.md
git commit -m "feat: manage Kafka publishing from the web UI"
```
