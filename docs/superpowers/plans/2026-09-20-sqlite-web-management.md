# SQLite Web Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace file-based device configuration with SQLite-backed device and point management, add a Gin JSON API and static HTML/CSS/JavaScript administration UI, and provide safe persistent device lifecycle control.

**Architecture:** Keep one Go process, with explicit packages for service configuration, logging, SQLite persistence, domain services, device runtime management, HTTP transport, and static web assets. Device and point configuration lives only in SQLite; TOML configures the server, database path, and logging. Runtime state and current values remain in memory and are exposed through read-only API snapshots.

**Tech Stack:** Go 1.21, Gin, `database/sql`, `modernc.org/sqlite`, `log/slog`, lumberjack v2, vanilla HTML/CSS/JavaScript, `httptest`.

## Global Constraints

- Preserve existing user changes and do not reformat unrelated files.
- Use `context.Context` as the first parameter for database, network, and lifecycle operations.
- Use parameterized SQL and SQLite transactions for related writes.
- Keep HTTP bound to the configured address; the default is `127.0.0.1:8080` and there is no authentication in this release.
- Configuration edits never restart a device automatically; the UI must show when a manual restart is required.
- Device start/stop state is persistent through the `enabled` column.
- CSV import is strict and atomic: one invalid row rejects the entire file.
- Do not persist current point values or high-frequency scan state in SQLite.
- Do not log request bodies, CSV content, secrets, or every point value on every scan.
- All new Go code must be formatted with `gofmt`.

---

## Planned File Structure

```text
cmd/modbus-scan/main.go                 application composition and shutdown
internal/appconfig/config.go            service TOML model, defaults, validation
internal/appconfig/config_test.go
internal/logging/logging.go              slog and lumberjack construction
internal/logging/logging_test.go
internal/model/device.go                 shared device and runtime DTOs
internal/model/point.go                  shared point DTOs and validation types
internal/store/store.go                  SQLite opening, pragmas, migrations
internal/store/device.go                 device persistence
internal/store/point.go                  point persistence and replacement
internal/store/store_test.go
internal/store/device_test.go
internal/store/point_test.go
internal/pointcsv/csv.go                 strict CSV parse and streaming export
internal/pointcsv/csv_test.go
internal/collector/collector.go          cancellable connection lifecycle/status hooks
internal/collector/collector_test.go
internal/runtime/manager.go              device lifecycle registry
internal/runtime/manager_test.go
internal/service/device.go               device use cases
internal/service/point.go                point and CSV use cases
internal/service/service_test.go
internal/httpapi/server.go               Gin engine and HTTP server wiring
internal/httpapi/response.go             stable response/error envelopes
internal/httpapi/device_handler.go        device endpoints
internal/httpapi/point_handler.go         point and CSV endpoints
internal/httpapi/httpapi_test.go
web/index.html
web/device.html
web/css/app.css
web/js/api.js
web/js/devices.js
web/js/device.js
configs/config.toml                       service-only example configuration
.gitignore
README.md
docs/point.csv的点位配置.md
```

---

### Task 1: Service Configuration and Logging

**Files:**
- Create: `internal/appconfig/config.go`
- Create: `internal/appconfig/config_test.go`
- Create: `internal/logging/logging.go`
- Create: `internal/logging/logging_test.go`
- Modify: `configs/config.toml`

**Interfaces:**
- Produces: `appconfig.Load(path string) (appconfig.Config, error)`.
- Produces: `logging.New(cfg appconfig.LogConfig) (*slog.Logger, io.Closer, error)`.
- Consumed by: application composition in Task 10.

- [ ] **Step 1: Write failing configuration tests**

Create table-driven tests covering defaults, a complete valid file, invalid host, port outside `1..65535`, invalid log level/format, empty database path, and non-positive timeout/rotation values.

```go
func TestLoadDefaults(t *testing.T) {
    path := writeConfig(t, ``)
    cfg, err := Load(path)
    if err != nil { t.Fatal(err) }
    if cfg.Server.Host != "127.0.0.1" || cfg.Server.Port != 8080 {
        t.Fatalf("server defaults = %s:%d", cfg.Server.Host, cfg.Server.Port)
    }
    if cfg.Database.Path != "data/modbus-scan.db" { t.Fatalf("db path = %q", cfg.Database.Path) }
}

func TestLoadRejectsInvalidPort(t *testing.T) {
    path := writeConfig(t, "[server]\nport = 70000\n")
    if _, err := Load(path); err == nil { t.Fatal("expected invalid port error") }
}
```

- [ ] **Step 2: Run the focused test and verify failure**

Run: `go test ./internal/appconfig -run TestLoad -v`

Expected: FAIL because `Load` and configuration types do not exist.

- [ ] **Step 3: Implement the configuration model**

Define exact public types and validation:

```go
type Config struct {
    Server   ServerConfig   `toml:"server"`
    Database DatabaseConfig `toml:"database"`
    Log      LogConfig      `toml:"log"`
}

type ServerConfig struct {
    Host                 string `toml:"host"`
    Port                 int    `toml:"port"`
    ReadHeaderTimeoutSec int    `toml:"read_header_timeout_sec"`
    ReadTimeoutSec       int    `toml:"read_timeout_sec"`
    WriteTimeoutSec      int    `toml:"write_timeout_sec"`
    IdleTimeoutSec       int    `toml:"idle_timeout_sec"`
    ShutdownTimeoutSec   int    `toml:"shutdown_timeout_sec"`
}

type DatabaseConfig struct {
    Path          string `toml:"path"`
    BusyTimeoutMs int    `toml:"busy_timeout_ms"`
}

type LogConfig struct {
    Level      string `toml:"level"`
    Format     string `toml:"format"`
    Console    bool   `toml:"console"`
    File       string `toml:"file"`
    MaxSizeMB  int    `toml:"max_size_mb"`
    MaxBackups int    `toml:"max_backups"`
    MaxAgeDays int    `toml:"max_age_days"`
    Compress   bool   `toml:"compress"`
}
```

Use `net.ParseIP` for literal IPs and allow `localhost` explicitly. Return wrapped, lower-case operational errors. Apply the approved defaults before TOML decoding.

- [ ] **Step 4: Run configuration tests and verify pass**

Run: `go test ./internal/appconfig -v`

Expected: PASS.

- [ ] **Step 5: Add logging tests**

Test text/JSON handlers, level filtering, console-only mode, file directory creation, and invalid file paths. Inject output writers through an unexported helper so tests do not mutate global logging.

```go
func TestNewJSONLoggerFiltersDebug(t *testing.T) {
    var buf bytes.Buffer
    logger, closeFn, err := newWithWriters(appconfig.LogConfig{Level: "info", Format: "json"}, &buf, nil)
    if err != nil { t.Fatal(err) }
    defer closeFn.Close()
    logger.Debug("hidden")
    logger.Info("visible")
    if strings.Contains(buf.String(), "hidden") || !strings.Contains(buf.String(), "visible") {
        t.Fatalf("unexpected log output %q", buf.String())
    }
}
```

- [ ] **Step 6: Add dependencies and implement logging**

Run: `go get gopkg.in/natefinch/lumberjack.v2`

Build `slog.TextHandler` or `slog.JSONHandler`; combine console and lumberjack writers with `io.MultiWriter`. Return an `io.Closer` owned by `main`. Never replace the process-global logger inside the package.

- [ ] **Step 7: Update the service configuration example and verify**

Replace device entries in `configs/config.toml` with the approved `[server]`, `[database]`, and `[log]` sections. Run:

```powershell
gofmt -w internal/appconfig internal/logging
go test ./internal/appconfig ./internal/logging
```

Expected: PASS.

- [ ] **Step 8: Commit**

```powershell
git add internal/appconfig internal/logging configs/config.toml go.mod go.sum
git commit -m "feat: add service configuration and logging"
```

---

### Task 2: SQLite Schema and Store

**Files:**
- Create: `internal/model/device.go`
- Create: `internal/model/point.go`
- Create: `internal/store/store.go`
- Create: `internal/store/device.go`
- Create: `internal/store/point.go`
- Create: `internal/store/store_test.go`
- Create: `internal/store/device_test.go`
- Create: `internal/store/point_test.go`

**Interfaces:**
- Produces: `store.Open(ctx context.Context, path string, busyTimeout time.Duration) (*store.Store, error)`.
- Produces CRUD methods using the DTOs below.
- Consumed by: service and runtime packages in Tasks 5–6.

- [ ] **Step 1: Define shared DTOs and failing store tests**

```go
type Device struct {
    ID             int64     `json:"id"`
    Name           string    `json:"name"`
    Enabled        bool      `json:"enabled"`
    Address        string    `json:"address"`
    Port           int       `json:"port"`
    SlaveID        int       `json:"slave_id"`
    ByteOrder      string    `json:"byte_order"`
    TimeoutSec     int       `json:"timeout_sec"`
    ScanIntervalMs int       `json:"scan_interval_ms"`
    ConfigVersion  int64     `json:"config_version"`
    CreatedAt      time.Time `json:"created_at"`
    UpdatedAt      time.Time `json:"updated_at"`
}

type Point struct {
    ID        int64     `json:"id"`
    DeviceID  int64     `json:"device_id"`
    TagName   string    `json:"tag_name"`
    RegType   string    `json:"reg_type"`
    Address   uint16    `json:"address"`
    DataType  string    `json:"data_type"`
    BitOffset int       `json:"bit_offset"`
    BitLen    int       `json:"bit_len"`
    Writeable bool      `json:"writeable"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}

type FieldError struct {
    Field   string `json:"field"`
    Message string `json:"message"`
}

type RowError struct {
    Row     int    `json:"row"`
    Field   string `json:"field"`
    Message string `json:"message"`
}
```

Write tests for empty schema creation, repeat opening, pragma enforcement, device CRUD, duplicate device name, point CRUD, per-device duplicate tag rejection, cascade delete, and config-version increments.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/store -v`

Expected: FAIL because store functions do not exist.

- [ ] **Step 3: Add SQLite dependency and schema**

Run: `go get modernc.org/sqlite`

`Open` must create the parent directory, open with the `sqlite` driver, set `MaxOpenConns(1)`, ping with context, apply `foreign_keys=ON`, `journal_mode=WAL`, and configured `busy_timeout`, then execute a versioned migration table plus the approved `devices` and `points` DDL. Return a Store with `Close() error`.

- [ ] **Step 4: Implement device persistence**

```go
func (s *Store) CreateDevice(ctx context.Context, d model.Device) (model.Device, error)
func (s *Store) GetDevice(ctx context.Context, id int64) (model.Device, error)
func (s *Store) ListDevices(ctx context.Context) ([]model.Device, error)
func (s *Store) UpdateDevice(ctx context.Context, d model.Device) (model.Device, error)
func (s *Store) SetDeviceEnabled(ctx context.Context, id int64, enabled bool) (model.Device, error)
func (s *Store) DeleteDevice(ctx context.Context, id int64) error
```

Map no rows to `store.ErrNotFound` and unique constraints to `store.ErrConflict`. `UpdateDevice` increments `config_version`; `SetDeviceEnabled` does not, because enabled state does not change loaded Modbus configuration.

- [ ] **Step 5: Implement point persistence**

```go
type PointFilter struct { Search, RegType string; Limit, Offset int }
type PointPage struct { Items []model.Point; Total int }

func (s *Store) CreatePoint(ctx context.Context, p model.Point) (model.Point, error)
func (s *Store) GetPoint(ctx context.Context, deviceID, pointID int64) (model.Point, error)
func (s *Store) ListPoints(ctx context.Context, deviceID int64, f PointFilter) (PointPage, error)
func (s *Store) ListAllPoints(ctx context.Context, deviceID int64) ([]model.Point, error)
func (s *Store) UpdatePoint(ctx context.Context, p model.Point) (model.Point, error)
func (s *Store) DeletePoint(ctx context.Context, deviceID, pointID int64) error
func (s *Store) ReplacePoints(ctx context.Context, deviceID int64, points []model.Point) error
```

Every point mutation and `ReplacePoints` increments the parent device version in the same transaction. `ReplacePoints` must roll back both delete and inserts on any error.

- [ ] **Step 6: Format and run store tests**

Run:

```powershell
gofmt -w internal/model internal/store
go test ./internal/store -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```powershell
git add internal/model internal/store go.mod go.sum
git commit -m "feat: add sqlite device and point store"
```

---

### Task 3: Strict Point Validation and CSV Codec

**Files:**
- Create: `internal/pointcsv/csv.go`
- Create: `internal/pointcsv/csv_test.go`
- Modify: `internal/config/point_config.go`
- Modify: `internal/config/point_config_test.go`
- Modify: `docs/point.csv的点位配置.md`

**Interfaces:**
- Produces: `config.ValidatePoint(model.Point) []model.FieldError`.
- Produces: `pointcsv.Parse(r io.Reader) ([]model.Point, []model.RowError, error)`.
- Produces: `pointcsv.Write(w io.Writer, points []model.Point) error`.
- Consumed by: point service and CSV handlers in Tasks 6 and 8.

- [ ] **Step 1: Write failing strict-validation tests**

Cover exact header matching, exactly seven columns, invalid boolean text, duplicate tags, invalid address/type/bit range, multi-register overflow at address `65535`, valid middle-two-bit extraction, CRLF, BOM, empty data, and aggregate row errors.

```go
func TestParseRejectsRegisterOverflow(t *testing.T) {
    input := "TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable\n" +
        "Tail,HoldingReg,65535,Double,0,0,false\n"
    _, rows, err := Parse(strings.NewReader(input))
    if err != nil { t.Fatal(err) }
    if len(rows) != 1 || rows[0].Field != "address" { t.Fatalf("errors = %#v", rows) }
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/pointcsv ./internal/config -v`

Expected: FAIL because strict codec and shared validator do not exist.

- [ ] **Step 3: Extract pure point validation**

Move enum, tag, bit-width, register-count, writeability, and address-end validation into pure functions operating on `model.Point`. Keep chunk optimization in `internal/config` temporarily to minimize unrelated churn. Ensure `Address + registerCount - 1` uses a wider integer before comparing with `65535`.

- [ ] **Step 4: Implement strict streaming CSV parsing and writing**

Use `csv.Reader.Read()` in a loop instead of `ReadAll`. Reject a mismatched header and rows with any column count other than seven. Parse booleans with `strconv.ParseBool` after requiring case-insensitive `true` or `false`. Return all row errors without returning partially valid points to callers. `Write` emits the canonical header and stable `RegType`, `Address`, `TagName` order.

- [ ] **Step 5: Keep CLI validation compatibility**

Change `LoadPointsFromCSV` to call the strict parser and return a failure when any row is invalid. Adapt its result to the existing `config.PointConfig` until all collector call sites move to shared model types.

- [ ] **Step 6: Run focused tests and update documentation**

Run:

```powershell
gofmt -w internal/config internal/pointcsv
go test ./internal/config ./internal/pointcsv -v
```

Expected: PASS. Update the CSV document to state strict all-or-nothing validation and address-end limits.

- [ ] **Step 7: Commit**

```powershell
git add internal/config internal/pointcsv docs/point.csv的点位配置.md
git commit -m "feat: enforce strict point csv validation"
```

---

### Task 4: Collector Lifecycle, Status, and Parsing Tests

**Files:**
- Modify: `internal/collector/collector.go`
- Create: `internal/collector/collector_test.go`
- Modify: `internal/udm/udm.go`
- Modify: `internal/udm/udm_test.go`

**Interfaces:**
- Produces: `collector.Factory` and `collector.Runner` boundaries used by runtime tests.
- Produces: status callbacks and explicit `Close()`.
- Consumed by: DeviceManager in Task 5.

- [ ] **Step 1: Add failing parser and cancellation tests**

Test ABCD/DCBA/CDAB/BADC for 32- and 64-bit data, coil LSB order, Int16/Int32 sign handling, two-bit extraction, short responses, and context cancellation during reconnect delay.

```go
func TestReconnectStopsOnContextCancel(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    cm := newTestConnManager(ctx, alwaysFailConnector{})
    done := make(chan struct{})
    go func() { defer close(done); cm.ReconnectWithBackoff() }()
    cancel()
    select {
    case <-done:
    case <-time.After(250 * time.Millisecond): t.Fatal("reconnect did not stop")
    }
}
```

- [ ] **Step 2: Run collector tests and verify failure**

Run: `go test ./internal/collector -v`

Expected: FAIL due to unavailable seams and uncancellable sleep.

- [ ] **Step 3: Introduce narrow Modbus interfaces**

```go
type Client interface {
    ReadCoils(address, quantity uint16) ([]byte, error)
    ReadDiscreteInputs(address, quantity uint16) ([]byte, error)
    ReadHoldingRegisters(address, quantity uint16) ([]byte, error)
    ReadInputRegisters(address, quantity uint16) ([]byte, error)
    WriteSingleCoil(address, value uint16) ([]byte, error)
    WriteSingleRegister(address, value uint16) ([]byte, error)
}

type Connector interface {
    Connect(ctx context.Context, cfg model.Device) (Client, io.Closer, error)
}

type Runner interface {
    Run(ctx context.Context)
    Close() error
    Values() map[string]udm.Value
}

type Factory interface {
    New(device model.Device, points []model.Point, sink StatusSink) (Runner, error)
}
```

Wrap goburrow/modbus behind a production connector. Do not let HTTP or runtime packages import goburrow types.

- [ ] **Step 4: Make lifecycle cancellable and observable**

Replace `time.Sleep` with a timer/select on context. Add `Close() error`, close handlers on replacement and shutdown, and use a status callback:

```go
type StatusEvent struct { State, LastError string; At time.Time }
type StatusSink func(StatusEvent)
```

The collector updates last-success time after a complete scan and does not log all values each cycle.

- [ ] **Step 5: Add UDM timestamp snapshots**

```go
type Value struct { Value any `json:"value"`; UpdatedAt time.Time `json:"updated_at"` }
func (u *UniversalDataModel) Update(tag string, value any, at time.Time)
func (u *UniversalDataModel) Snapshot() map[string]Value
```

Return a copied map and preserve race-safe access.

- [ ] **Step 6: Run focused and race tests**

Run:

```powershell
gofmt -w internal/collector internal/udm
go test ./internal/collector ./internal/udm -v
go test -race ./internal/collector ./internal/udm
```

Expected: PASS.

- [ ] **Step 7: Commit**

```powershell
git add internal/collector internal/udm
git commit -m "refactor: make collector lifecycle cancellable"
```

---

### Task 5: Device Runtime Manager

**Files:**
- Create: `internal/runtime/manager.go`
- Create: `internal/runtime/manager_test.go`

**Interfaces:**
- Consumes: store device/point reads and collector factory.
- Produces: lifecycle and runtime snapshot methods for services/API.

- [ ] **Step 1: Write failing lifecycle tests with fakes**

Cover startup of enabled devices, failure isolation, idempotent start/stop, restart loading a newer config version, configuration pending flag, duplicate concurrent operations, retained last values after stop, and `StopAll` waiting for runners.

```go
type ConfigSource interface {
    GetDevice(ctx context.Context, id int64) (model.Device, error)
    ListDevices(ctx context.Context) ([]model.Device, error)
    ListAllPoints(ctx context.Context, deviceID int64) ([]model.Point, error)
    SetDeviceEnabled(ctx context.Context, id int64, enabled bool) (model.Device, error)
}

type Runner interface {
    Run(ctx context.Context)
    Close() error
    Values() map[string]udm.Value
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/runtime -v`

Expected: FAIL because `Manager` is undefined.

- [ ] **Step 3: Implement manager state and snapshots**

```go
type Snapshot struct {
    DeviceID            int64                `json:"device_id"`
    State               string               `json:"state"`
    LastError           string               `json:"last_error,omitempty"`
    LastCollectedAt     *time.Time            `json:"last_collected_at,omitempty"`
    LoadedConfigVersion int64                 `json:"loaded_config_version"`
    ConfigPending       bool                  `json:"config_pending"`
    Values              map[string]udm.Value `json:"values,omitempty"`
}

func (m *Manager) Start(ctx context.Context, id int64) error
func (m *Manager) Stop(ctx context.Context, id int64) error
func (m *Manager) Restart(ctx context.Context, id int64) error
func (m *Manager) StartEnabled(ctx context.Context) []error
func (m *Manager) StopAll(ctx context.Context) error
func (m *Manager) Snapshot(ctx context.Context, id int64) (Snapshot, error)
```

Use a short per-device operation mutex/state object, and never hold the registry mutex during store or network calls. Keep runtime status even after a device stops.

- [ ] **Step 4: Run lifecycle and race tests**

Run:

```powershell
gofmt -w internal/runtime
go test ./internal/runtime -v
go test -race ./internal/runtime
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/runtime
git commit -m "feat: add persistent device runtime manager"
```

---

### Task 6: Device and Point Application Services

**Files:**
- Create: `internal/service/device.go`
- Create: `internal/service/point.go`
- Create: `internal/service/service_test.go`

**Interfaces:**
- Consumes: store, point validator/CSV codec, and runtime manager.
- Produces: transport-neutral CRUD/lifecycle use cases.

- [ ] **Step 1: Write failing service tests**

Test device normalization and bounds (`port`, `slave_id`, timeouts, byte order), create/update/delete conflicts, delete stopping a running device first, point ownership, pagination bounds, strict import rollback, export ordering, and runtime status composition.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/service -v`

Expected: FAIL because services are undefined.

- [ ] **Step 3: Implement device service**

```go
type DeviceService struct { store DeviceStore; runtime DeviceRuntime }
func (s *DeviceService) Create(ctx context.Context, in model.Device) (model.Device, error)
func (s *DeviceService) Update(ctx context.Context, id int64, in model.Device) (model.Device, error)
func (s *DeviceService) Delete(ctx context.Context, id int64) error
func (s *DeviceService) List(ctx context.Context) ([]DeviceView, error)
func (s *DeviceService) Get(ctx context.Context, id int64) (DeviceView, error)
func (s *DeviceService) Start(ctx context.Context, id int64) error
func (s *DeviceService) Stop(ctx context.Context, id int64) error
func (s *DeviceService) Restart(ctx context.Context, id int64) error
```

`DeviceView` combines persisted configuration with runtime status and point count without exposing internal implementation types:

```go
type DeviceView struct {
    Device      model.Device    `json:"device"`
    PointCount  int             `json:"point_count"`
    Runtime     runtime.Snapshot `json:"runtime"`
}
```

- [ ] **Step 4: Implement point service**

```go
func (s *PointService) Create(ctx context.Context, deviceID int64, p model.Point) (model.Point, error)
func (s *PointService) Update(ctx context.Context, deviceID, pointID int64, p model.Point) (model.Point, error)
func (s *PointService) Delete(ctx context.Context, deviceID, pointID int64) error
func (s *PointService) List(ctx context.Context, deviceID int64, f store.PointFilter) (store.PointPage, error)
func (s *PointService) Import(ctx context.Context, deviceID int64, r io.Reader) (int, []model.RowError, error)
func (s *PointService) Export(ctx context.Context, deviceID int64, w io.Writer) error
```

- [ ] **Step 5: Run tests and commit**

Run:

```powershell
gofmt -w internal/service
go test ./internal/service -v
```

Expected: PASS.

```powershell
git add internal/service
git commit -m "feat: add device and point services"
```

---

### Task 7: Gin Server and Device API

**Files:**
- Create: `internal/httpapi/server.go`
- Create: `internal/httpapi/response.go`
- Create: `internal/httpapi/device_handler.go`
- Create: `internal/httpapi/httpapi_test.go`

**Interfaces:**
- Consumes: device service.
- Produces: Gin engine and configured `http.Server`.

- [ ] **Step 1: Add Gin dependency and failing API tests**

Run: `go get github.com/gin-gonic/gin`

Use fake services and `httptest` to cover device list/create/get/update/delete, start/stop/restart, malformed JSON, unknown JSON fields, service error mapping, and stable response envelopes.

```go
func TestCreateDeviceRejectsUnknownField(t *testing.T) {
    r := newTestRouter(fakeDeviceService{})
    req := httptest.NewRequest(http.MethodPost, "/api/v1/devices", strings.NewReader(`{"name":"x","unknown":1}`))
    req.Header.Set("Content-Type", "application/json")
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)
    if rec.Code != http.StatusBadRequest { t.Fatalf("status = %d", rec.Code) }
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/httpapi -run 'Test.*Device' -v`

Expected: FAIL because the router is undefined.

- [ ] **Step 3: Implement response/error helpers and middleware**

Define `success(c, status, data)` and `failure(c, status, code, message, details)`. Add recovery, request ID, structured access logging, and explicit body-size limiting. Do not use Gin's default logger.

- [ ] **Step 4: Implement device handlers and routes**

Register the approved device routes under `/api/v1`. Parse IDs with `strconv.ParseInt`, decode JSON with `DisallowUnknownFields`, and pass `c.Request.Context()` through all service calls.

- [ ] **Step 5: Implement configured HTTP server construction**

```go
func NewServer(cfg appconfig.ServerConfig, handler http.Handler) *http.Server
func NewRouter(logger *slog.Logger, devices DeviceService, points PointService, webFS fs.FS) *gin.Engine
```

Set all approved timeouts and compose `Addr` with `net.JoinHostPort`.

- [ ] **Step 6: Run API tests and commit**

Run:

```powershell
gofmt -w internal/httpapi
go test ./internal/httpapi -v
```

Expected: PASS.

```powershell
git add internal/httpapi go.mod go.sum
git commit -m "feat: add gin device management api"
```

---

### Task 8: Point, CSV, Status, and Value API

**Files:**
- Create: `internal/httpapi/point_handler.go`
- Modify: `internal/httpapi/device_handler.go`
- Modify: `internal/httpapi/httpapi_test.go`

**Interfaces:**
- Consumes: point service and runtime snapshots.
- Produces: remaining approved `/api/v1` endpoints.

- [ ] **Step 1: Write failing endpoint tests**

Cover point pagination/search/filter, create/get/update/delete, 10 MiB import cap, missing upload, invalid CSV row detail mapping, successful replacement count, export content type/disposition/order, status, and values.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./internal/httpapi -run 'Test(Point|CSV|Status|Values)' -v`

Expected: FAIL because handlers/routes are missing.

- [ ] **Step 3: Implement point CRUD and read endpoints**

Clamp `limit` to `1..200`, default to `50`, and reject negative offsets. Return status and values as separate payloads so polling values does not resend device configuration.

- [ ] **Step 4: Implement safe import/export**

Wrap upload bodies with `http.MaxBytesReader`, require multipart `file`, and map row errors into the approved details array. Set:

```text
Content-Type: text/csv; charset=utf-8
Content-Disposition: attachment; filename="<sanitized>-points.csv"
```

Sanitize filenames to ASCII letters, digits, `_`, and `-`, falling back to `device-<id>`.

- [ ] **Step 5: Run tests and commit**

Run:

```powershell
gofmt -w internal/httpapi
go test ./internal/httpapi -v
```

Expected: PASS.

```powershell
git add internal/httpapi
git commit -m "feat: add point csv and runtime api"
```

---

### Task 9: Static Administration UI

**Files:**
- Create: `web/index.html`
- Create: `web/device.html`
- Create: `web/css/app.css`
- Create: `web/js/api.js`
- Create: `web/js/devices.js`
- Create: `web/js/device.js`
- Modify: `internal/httpapi/server.go`
- Modify: `internal/httpapi/httpapi_test.go`

**Interfaces:**
- Consumes: JSON API from Tasks 7–8.
- Produces: offline-capable static management pages.

- [ ] **Step 1: Write failing static-route tests**

Test `/`, `/device.html`, CSS, and JavaScript return `200`, correct content types, and no Go template rendering. Test missing assets return `404` rather than the index page.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/httpapi -run TestStatic -v`

Expected: FAIL because web assets/routes do not exist.

- [ ] **Step 3: Implement shared API helper**

`web/js/api.js` exports an `api` object with `request`, `get`, `post`, `put`, `delete`, multipart upload, download URL, normalized error handling, and request cancellation. It must not store configuration in local storage.

- [ ] **Step 4: Implement device list page**

Create accessible semantic HTML and vanilla JS for table rendering, empty state, create/edit form, delete confirmation, lifecycle buttons, status badges, errors, point counts, and pending-config badges. Poll every 2 seconds while visible and every 10 seconds while hidden; prevent overlapping polls.

- [ ] **Step 5: Implement device detail page**

Read a positive `id` query parameter. Implement device summary, paged/searched/filtered points, point create/edit/delete, CSV upload with confirmation and row errors, export link, runtime values table, and lifecycle controls. Disable controls during writes and show a non-alert notification area.

- [ ] **Step 6: Add responsive local styling**

Use only `web/css/app.css`; no CDN, fonts, frameworks, inline event handlers, or generated bundles. Ensure keyboard focus is visible and forms have labels.

- [ ] **Step 7: Serve assets and verify**

Use `os.DirFS("web")` for this repository layout, passed into `NewRouter`; keep the router testable with `fstest.MapFS`. Run:

```powershell
go test ./internal/httpapi -v
```

Expected: PASS.

- [ ] **Step 8: Manual UI smoke check**

Start the application against a temporary database and verify: empty state, add/edit device, point form, CSV import error rendering, export download, start/stop/restart button states, pending configuration marker, and value polling. Record the exact command in the commit message body or task notes.

- [ ] **Step 9: Commit**

```powershell
git add web internal/httpapi
git commit -m "feat: add static device management ui"
```

---

### Task 10: Application Composition and Graceful Shutdown

**Files:**
- Modify: `cmd/modbus-scan/main.go`
- Modify: `build.bat`
- Modify: `.gitignore`
- Create: `cmd/modbus-scan/main_test.go`

**Interfaces:**
- Consumes all earlier package constructors.
- Produces the complete executable lifecycle.

- [ ] **Step 1: Extract a testable run function and write failing tests**

```go
func run(ctx context.Context, args []string, deps dependencies) error
```

Test missing config, empty database startup, enabled-device startup failure isolation, HTTP listen failure, cancellation order, shutdown timeout, and closing store/logger. Keep `main()` limited to signal context, invoking `run`, and exit logging.

- [ ] **Step 2: Run command tests and verify failure**

Run: `go test ./cmd/modbus-scan -v`

Expected: FAIL because composition seams are absent.

- [ ] **Step 3: Implement composition**

Parse only `-config` for normal service startup while preserving `-validate -csv` as an early offline path. Construct configuration, logger, store, collector factory, runtime manager, services, router, and `http.Server` in dependency order. On cancellation, call `Server.Shutdown`, then `Manager.StopAll`, then close store and logger. Combine multiple shutdown errors without hiding earlier failures.

- [ ] **Step 4: Update build and ignored runtime artifacts**

Keep Windows amd64 build behavior. Add exact ignore rules:

```gitignore
/data/
/logs/
*.db
*.db-shm
*.db-wal
```

Do not ignore example configuration or CSV files.

- [ ] **Step 5: Run focused integration checks**

Run:

```powershell
gofmt -w cmd/modbus-scan
go test ./cmd/modbus-scan ./internal/... -v
go build -o modbus-scan.exe ./cmd/modbus-scan
```

Expected: PASS and executable created.

- [ ] **Step 6: Commit**

```powershell
git add cmd/modbus-scan build.bat .gitignore
git commit -m "feat: compose sqlite web management service"
```

---

### Task 11: Documentation and Full Verification

**Files:**
- Modify: `README.md`
- Modify: `docs/项目设计开发技术文档.md`
- Modify: `docs/point.csv的点位配置.md`

**Interfaces:**
- Documents the final behavior and operator workflow.

- [ ] **Step 1: Update operator documentation**

Document service configuration fields/defaults, database/log paths, startup command, first-run empty state, page URL, device and point workflows, manual restart requirement, persistent enable state, strict import/export behavior, local-only/no-auth warning, shutdown, backup of the SQLite file, and troubleshooting.

- [ ] **Step 2: Update engineering documentation**

Document package responsibilities, database ownership, runtime lifecycle, status meanings, API summary, config-version semantics, testing commands, and why current values stay in memory.

- [ ] **Step 3: Verify documentation commands**

Run each documented PowerShell command that does not require a real PLC. Verify the example configuration loads and the empty database starts the page.

- [ ] **Step 4: Run complete automated verification**

Use a repository-local writable Go cache if required by the environment:

```powershell
$env:GOCACHE = Join-Path (Get-Location) '.gocache'
gofmt -w cmd internal
go test ./...
go test -race ./...
go vet ./...
go build -o modbus-scan.exe ./cmd/modbus-scan
```

Expected: every command exits `0`. Remove only the verification cache and temporary build artifact after resolving and checking their absolute paths inside the repository.

- [ ] **Step 5: Run final manual acceptance**

With no PLC required, verify database creation, empty page, device/point CRUD, strict CSV rejection, successful CSV replacement/export, persistent stopped state across restart, and pending-config indicator. With a simulator when available, verify online/offline transitions, values, restart loading changes, and graceful stop.

- [ ] **Step 6: Review the final diff for scope and secrets**

Run:

```powershell
git status --short
git diff --check
git diff --stat
rg -n -i "password|secret|token|api[_-]?key" --glob '!go.sum' .
```

Confirm no database, logs, binaries, caches, credentials, or unrelated formatting changes are included.

- [ ] **Step 7: Commit**

```powershell
git add README.md docs/项目设计开发技术文档.md docs/point.csv的点位配置.md
git commit -m "docs: document sqlite web management"
```
