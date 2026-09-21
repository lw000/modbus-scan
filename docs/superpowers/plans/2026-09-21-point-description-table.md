# Point Description and Table Layout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `subagent-driven-development` (recommended) or `executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist an optional point description, use numeric `0/1` writeability across API and CSV, and improve the point table and dialog layout.

**Architecture:** Extend the existing `model.Point` contract and SQLite schema with an additive migration, then let the current service and HTTP layers carry the fields without introducing new abstractions. Keep the collector's legacy runtime configuration boolean by converting `model.Point.Writeable == 1` at its boundary. Update the static HTML/CSS/JavaScript UI and strict CSV codecs to expose the same contract.

**Tech Stack:** Go 1.21, `database/sql`, modernc SQLite, Gin, `encoding/csv`, embedded HTML/CSS/JavaScript, Go `testing`.

## Global Constraints

- `Description` is optional, trimmed, and limited to 255 characters.
- JSON API `writeable` is an integer and accepts only `0` or `1`; JSON booleans are rejected by decoding.
- CSV header is exactly `TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description`.
- CSV `Writeable` accepts and emits only `0` or `1`; seven-column files are invalid.
- Existing SQLite data must survive the additive migration and receive an empty description.
- No new third-party dependencies.
- Preserve unrelated working-tree changes.

---

## File Structure

- `internal/model/point.go`: canonical persisted/API point shape.
- `internal/config/point_config.go`: shared point validation and legacy runtime/CSV compatibility.
- `internal/pointcsv/csv.go`: strict eight-column Web/CLI import and export contract.
- `internal/store/store.go`: idempotent schema creation and version-2 description migration.
- `internal/store/point.go`: point SQL projections and mutations.
- `internal/collector/runtime.go`: integer-to-boolean conversion at the collector boundary.
- `internal/collector/collector.go`: collector permission check remains boolean on `config.PointConfig`.
- `web/device.html`, `web/css/app.css`, `web/js/device.js`: table and dialog behavior.
- Existing `_test.go` files beside each package: regression coverage.
- `README.md`, `docs/AI_DEV.md`, `docs/AI_DEV_RULE.md`, `docs/point.csv的点位配置.md`, `docs/项目设计开发技术文档.md`, `configs/*.csv`: public contract and examples.

### Task 1: Canonical Model and Validation

**Files:**
- Modify: `internal/model/point.go`
- Modify: `internal/config/point_config.go`
- Test: `internal/config/point_config_test.go`

**Interfaces:**
- Produces: `model.Point.Description string` and `model.Point.Writeable int`.
- Produces: `config.ValidatePoint(model.Point) []model.FieldError`, including `description` length and `writeable` enumeration validation.

- [ ] **Step 1: Write failing validation tests**

Add focused tests that construct valid points and assert the two new errors:

```go
func TestValidatePointRejectsInvalidWriteable(t *testing.T) {
	point := model.Point{TagName: "A", RegType: "HoldingReg", DataType: "UInt16", BitLen: 16, Writeable: 2}
	assertFieldError(t, ValidatePoint(point), "writeable")
}

func TestValidatePointRejectsLongDescription(t *testing.T) {
	point := model.Point{TagName: "A", RegType: "HoldingReg", DataType: "UInt16", BitLen: 16, Description: strings.Repeat("测", 256)}
	assertFieldError(t, ValidatePoint(point), "description")
}
```

Use the file's existing assertion style if `assertFieldError` has a different local name.

- [ ] **Step 2: Run the tests and verify RED**

Run: `go test ./internal/config -run 'TestValidatePointRejects(InvalidWriteable|LongDescription)'`

Expected: compile failure because `Writeable` is still `bool` and/or assertions fail because description is not validated.

- [ ] **Step 3: Implement the model and validation contract**

Change the point fields to:

```go
Description string    `json:"description"`
Writeable   int       `json:"writeable"`
```

At the start of `ValidatePoint`, normalize the caller-owned value only in service code; validation itself checks:

```go
if utf8.RuneCountInString(strings.TrimSpace(point.Description)) > 255 {
	errors = append(errors, model.FieldError{Field: "description", Message: "must be at most 255 characters"})
}
if point.Writeable != 0 && point.Writeable != 1 {
	errors = append(errors, model.FieldError{Field: "writeable", Message: "must be 0 or 1"})
}
if point.Writeable == 1 && point.RegType != "HoldingReg" && point.RegType != "CoilStatus" {
	errors = append(errors, model.FieldError{Field: "writeable", Message: "only holding registers and coils can be writeable"})
}
```

Import `unicode/utf8`. Replace model-point boolean test assignments with `0/1`; keep the separate legacy `PointConfig.Writeable bool` unchanged.

- [ ] **Step 4: Run package tests and verify GREEN**

Run: `go test ./internal/config`

Expected: PASS.

- [ ] **Step 5: Commit the task**

```powershell
git add internal/model/point.go internal/config/point_config.go internal/config/point_config_test.go
git commit -m "feat: define numeric point writeability"
```

### Task 2: SQLite Migration and Description Persistence

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/store/point.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: `model.Point.Description` and integer `model.Point.Writeable` from Task 1.
- Produces: schema version 2 with `points.description TEXT NOT NULL DEFAULT ''` and full CRUD round trips.

- [ ] **Step 1: Write failing migration and round-trip tests**

Create a version-1 database using `database/sql`, reopen it through `store.Open`, and assert the old row survives with an empty description. Extend point mutation coverage with:

```go
created, err := s.CreatePoint(ctx, model.Point{
	DeviceID: device.ID, TagName: "Speed", Description: "主轴速度",
	RegType: "HoldingReg", Address: 1, DataType: "UInt16", BitLen: 16, Writeable: 1,
})
if err != nil { t.Fatal(err) }
if created.Description != "主轴速度" || created.Writeable != 1 {
	t.Fatalf("created = %#v", created)
}
```

The migration fixture must have the exact existing version-1 `points` columns and a row inserted before calling `Open`.

- [ ] **Step 2: Run store tests and verify RED**

Run: `go test ./internal/store -run 'Test(OpenMigratesPointDescription|PointMutationsIncrementVersionAndCascade)'`

Expected: FAIL because `description` is absent from the schema and SQL projection.

- [ ] **Step 3: Add the idempotent migration**

Keep the base `CREATE TABLE` statement at the version-1 shape, then run this transactional migration after executing that schema:

```go
func (s *Store) migratePointDescription(ctx context.Context) error {
	var applied int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=2`).Scan(&applied); err != nil {
		return fmt.Errorf("check migration 2: %w", err)
	}
	if applied != 0 { return nil }
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return fmt.Errorf("begin migration 2: %w", err) }
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `ALTER TABLE points ADD COLUMN description TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("add point description: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(2, CURRENT_TIMESTAMP)`); err != nil {
		return fmt.Errorf("record migration 2: %w", err)
	}
	if err := tx.Commit(); err != nil { return fmt.Errorf("commit migration 2: %w", err) }
	return nil
}
```

Call it from `migrate` after the version-1 schema executes. A fresh database is therefore created at version 1 and immediately migrated to version 2; a reopened version-2 database skips the `ALTER`.

- [ ] **Step 4: Extend all point SQL**

Use one consistent projection:

```go
const pointColumns = `id, device_id, tag_name, description, reg_type, address, data_type, bit_offset, bit_len, writeable, created_at, updated_at`
```

Scan directly into `p.Writeable`, and add `description` to `INSERT`, `UPDATE`, and `ReplacePoints` statements and arguments. Do not convert integer values to booleans.

- [ ] **Step 5: Run store tests and verify GREEN**

Run: `go test ./internal/store`

Expected: PASS, including opening the migrated database twice.

- [ ] **Step 6: Commit the task**

```powershell
git add internal/store/store.go internal/store/point.go internal/store/store_test.go
git commit -m "feat: persist point descriptions"
```

### Task 3: CSV, Service, HTTP, and Collector Boundary

**Files:**
- Modify: `internal/pointcsv/csv.go`
- Modify: `internal/pointcsv/csv_test.go`
- Modify: `internal/service/point.go`
- Modify: `internal/service/service_test.go`
- Modify: `internal/httpapi/httpapi_test.go`
- Modify: `cmd/modbus-scan/main_test.go`
- Modify: `internal/collector/runtime.go`
- Modify: tests containing model-point `Writeable` literals under `internal/collector` and `internal/runtime`
- Modify: legacy CSV expectations in `internal/config/point_config.go` and `internal/config/point_config_test.go`

**Interfaces:**
- Consumes: model and validation from Task 1; persistence from Task 2.
- Produces: strict eight-column CSV and numeric JSON behavior while preserving `config.PointConfig.Writeable bool` inside the collector.

- [ ] **Step 1: Write failing CSV tests**

Set the test header to:

```go
const header = "TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description\n"
```

Add valid input `Running,CoilStatus,1,Bool,0,0,1,运行状态`, assert `Description` and `Writeable == 1`, assert `true` produces a `writeable` row error, assert 256 runes produces a `description` row error, and assert exported output contains `,1,运行状态`.

- [ ] **Step 2: Run CSV tests and verify RED**

Run: `go test ./internal/pointcsv`

Expected: FAIL because the parser still requires seven columns and boolean text.

- [ ] **Step 3: Implement strict CSV parsing and writing**

Use:

```go
var canonicalHeader = []string{"TagName", "RegType", "Address", "DataType", "BitOffset", "BitLen", "Writeable", "Description"}
```

Parse field 6 with `strconv.Atoi`, reject values other than `0/1`, assign `Description: strings.TrimSpace(record[7])`, then call `config.ValidatePoint`. Export with `strconv.Itoa(point.Writeable)` and `point.Description`. Update the exact-column error from seven to eight.

- [ ] **Step 4: Normalize descriptions in create and update**

Before validation in `PointService.Create` and `PointService.Update`:

```go
point.Description = strings.TrimSpace(point.Description)
```

Add the `strings` import. CSV parsing already trims the description.

- [ ] **Step 5: Add an HTTP decoding regression test**

Send one create request containing `"writeable": true` and assert HTTP 400; send another containing `"writeable": 1, "description": "泵运行"` through a capturing stub and assert the stub receives the numeric value and description. Use the current `testRouter` and response conventions.

- [ ] **Step 6: Update the collector boundary**

In `internal/collector/runtime.go`, construct the legacy runtime config with:

```go
Writeable: point.Writeable == 1,
```

Keep `config.PointConfig.Writeable bool` and `collector.go`'s `if !targetPt.Writeable` check unchanged. Update model-point test literals from booleans to integers.

- [ ] **Step 7: Update remaining CSV fixtures and legacy loader rules**

Update service, HTTP stub, CLI, and config fixtures to the eight-column header and `0/1` records. Make the legacy `LoadPointsFromCSV` path require the same eight columns and map field 6 using:

```go
writeableValue, err := strconv.Atoi(strings.TrimSpace(record[6]))
if err != nil || (writeableValue != 0 && writeableValue != 1) {
	rowErrors = append(rowErrors, fmt.Errorf("line %d: Writeable must be 0 or 1", lineNumber))
	continue
}
writeable := writeableValue == 1
description := strings.TrimSpace(record[7])
```

If legacy `PointConfig` does not consume descriptions at runtime, add `Description string` there so validation and documented CSV round trips do not discard it.

- [ ] **Step 8: Run affected tests and verify GREEN**

Run: `go test ./internal/pointcsv ./internal/service ./internal/httpapi ./internal/collector ./internal/runtime ./internal/config ./cmd/modbus-scan`

Expected: PASS.

- [ ] **Step 9: Commit the task**

```powershell
git add internal/pointcsv internal/service internal/httpapi internal/collector internal/runtime internal/config cmd/modbus-scan/main_test.go
git commit -m "feat: use numeric writeability in point APIs"
```

### Task 4: Point Table and Dialog UI

**Files:**
- Modify: `web/device.html`
- Modify: `web/css/app.css`
- Modify: `web/js/device.js`
- Test: `web/embed_test.go`

**Interfaces:**
- Consumes: API point fields `description string` and `writeable int`.
- Produces: description column, sticky action column, horizontal scrolling, and responsive dialog layout.

- [ ] **Step 1: Write failing static-resource tests**

Extend `web/embed_test.go` assertions to require:

```go
`data-column="description"`,
`id="description"`,
`maxlength="255"`,
`class="form-row two-columns"`,
`class="writeable-field"`,
`class="actions dialog-actions"`,
`class="sticky-actions"`,
```

Require the script fragments `esc(point.description)`, `point.writeable === 1`, `? 1 : 0`, and the CSS selectors `.sticky-actions`, `.dialog-actions`, and `.two-columns`.

- [ ] **Step 2: Run Web tests and verify RED**

Run: `go test ./web`

Expected: FAIL listing the absent description and layout markers.

- [ ] **Step 3: Update the table markup and rendering**

Insert a description `<col>` and `<th>` after TagName. Add `class="sticky-actions"` to the operation `<th>`. Render rows in the same order, escape the description, render numeric writeability, and mark the last cell:

```js
`<td>${esc(point.tag_name)}</td><td>${esc(point.description)}</td>`
`<td>${point.writeable}</td>`
`<td class="sticky-actions"><button ...` 
```

Include `["description", "description"]` in edit-field assignment. Use:

```js
document.querySelector("#writeable").checked = point.writeable === 1;
writeable: document.querySelector("#writeable").checked ? 1 : 0,
description: document.querySelector("#description").value,
```

- [ ] **Step 4: Restructure the dialog**

Add `<label>描述<input id="description" maxlength="255"></label>`. Wrap address/data type and BitOffset/BitLen in separate `<div class="form-row two-columns">`. Use `<label class="writeable-field">允许写入<input id="writeable" type="checkbox"></label>` and `<div class="actions dialog-actions">` for buttons.

- [ ] **Step 5: Add responsive and sticky CSS**

Add rules equivalent to:

```css
#points-table{table-layout:fixed;min-width:82rem}
#points-table .sticky-actions{position:sticky;right:0;background:#fff;z-index:2;box-shadow:-4px 0 6px #0001}
#points-table thead .sticky-actions{z-index:3}
.form-row.two-columns{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:.75rem}
.writeable-field{display:flex;align-items:center;gap:.5rem}
.writeable-field input{margin:0}
.dialog-actions{justify-content:flex-end}
@media(max-width:700px){.form-row.two-columns{grid-template-columns:1fr}}
```

Keep `.table-scroll { overflow:auto }` so both scrollbars remain available.

- [ ] **Step 6: Run Web tests and verify GREEN**

Run: `go test ./web`

Expected: PASS.

- [ ] **Step 7: Commit the task**

```powershell
git add web/device.html web/css/app.css web/js/device.js web/embed_test.go
git commit -m "feat: improve point table and dialog layout"
```

### Task 5: Samples, Documentation, and Full Verification

**Files:**
- Modify: `configs/points.csv`
- Modify: `configs/points-coil.csv`
- Modify: `README.md`
- Modify: `docs/AI_DEV.md`
- Modify: `docs/AI_DEV_RULE.md`
- Modify: `docs/point.csv的点位配置.md`
- Modify: `docs/项目设计开发技术文档.md`

**Interfaces:**
- Consumes: finalized API/CSV/UI contract from Tasks 1–4.
- Produces: copyable examples and verification evidence.

- [ ] **Step 1: Update samples and documentation**

Change every current seven-column header to:

```csv
TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description
```

Append an empty description field to sample rows, convert `true/false` to `1/0`, document `0 = read-only` and `1 = writeable`, and document the optional 255-character description. Update Go structure examples from `Writeable bool` to `Writeable int` where they describe `model.Point`; preserve boolean wording only for the collector's internal `PointConfig` after explaining the boundary conversion.

- [ ] **Step 2: Format all changed Go files**

Run:

```powershell
gofmt -w cmd/modbus-scan/main_test.go internal/model/point.go internal/config/point_config.go internal/config/point_config_test.go internal/pointcsv/csv.go internal/pointcsv/csv_test.go internal/store/store.go internal/store/point.go internal/store/store_test.go internal/service/point.go internal/service/service_test.go internal/httpapi/httpapi_test.go internal/collector/runtime.go internal/collector/runtime_test.go internal/runtime/manager_test.go web/embed_test.go
```

Expected: command exits 0 and only formats listed files.

- [ ] **Step 3: Run the complete test suite**

Run: `$env:GOCACHE=(Join-Path (Get-Location) '.gocache'); go test ./...`

Expected: PASS for every package.

- [ ] **Step 4: Run static analysis**

Run: `$env:GOCACHE=(Join-Path (Get-Location) '.gocache'); go vet ./...`

Expected: exits 0 with no diagnostics.

- [ ] **Step 5: Build to a disposable workspace artifact**

Run: `$env:GOCACHE=(Join-Path (Get-Location) '.gocache'); go build -o tmp-modbus-scan.exe ./cmd/modbus-scan`

Expected: exits 0 and creates `tmp-modbus-scan.exe`; do not commit the binary or `.gocache`.

- [ ] **Step 6: Inspect the final diff**

Run: `git diff --check` and `git status --short`.

Expected: no whitespace errors; only scoped source, test, sample, and documentation changes plus pre-existing user changes are shown.

- [ ] **Step 7: Commit documentation separately if authorized**

```powershell
git add configs/points.csv configs/points-coil.csv README.md docs/AI_DEV.md docs/AI_DEV_RULE.md docs/point.csv的点位配置.md docs/项目设计开发技术文档.md
git commit -m "docs: document point descriptions and writeability"
```
