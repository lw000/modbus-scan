# Kafka Form Layout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render Kafka configuration forms with the requested compact layout and make new device full-publish intervals default to one second.

**Architecture:** Keep the existing HTML forms, JavaScript bindings, and API contract. Reuse the shared responsive two-column grid, add narrowly scoped Kafka form classes, and change only the SQLite schema default used when new device Kafka rows are inserted.

**Tech Stack:** Go 1.21, embedded HTML/CSS/JavaScript, SQLite, Go `testing` package.

## Global Constraints

- Existing device Kafka interval values remain unchanged.
- New device Kafka configurations default `full_interval_sec` to `1`.
- The valid interval range remains 1 through 86400 seconds.
- Full and change modes remain mutually exclusive.
- Kafka API fields and response shapes remain unchanged.
- Desktop Kafka settings use two columns; narrow screens fall back to one column.

---

## File Structure

- `internal/store/store_test.go`: verifies the persisted default for newly created devices.
- `internal/store/database.go`: defines the SQLite default applied to new device Kafka configuration rows.
- `web/embed_test.go`: verifies Kafka form layout hooks and the static one-second input fallback.
- `web/index.html`: groups global Kafka fields into the responsive two-column grid.
- `web/device.html`: marks device Kafka controls for inline layout and updates the static fallback.
- `web/css/app.css`: supplies Kafka-specific responsive presentation.

### Task 1: Default New Device Full-Publish Interval to One Second

**Files:**
- Modify: `internal/store/store_test.go:119`
- Modify: `internal/store/database.go:164`

**Interfaces:**
- Consumes: `Store.CreateDevice(context.Context, model.Device)` and `Store.GetDeviceKafkaConfig(context.Context, int64)`.
- Produces: newly inserted `device_kafka_configs` rows with `FullIntervalSec == 1`.

- [ ] **Step 1: Change the persistence assertion to the required default**

```go
if cfg.Enabled || cfg.Topic != "" || cfg.Mode != model.KafkaModeChange || cfg.FullIntervalSec != 1 {
	t.Fatalf("default config = %#v", cfg)
}
```

- [ ] **Step 2: Run the focused test and verify RED**

Run: `go test ./internal/store -run TestDeviceKafkaConfigDefaultsUpdatesAndCascades -count=1`

Expected: FAIL because `FullIntervalSec` is still `60`.

- [ ] **Step 3: Change the SQLite default used for new rows**

In `migrateDeviceKafkaConfigs`, change the column definition to:

```sql
full_interval_sec INTEGER NOT NULL DEFAULT 1,
```

Do not add a migration that updates existing rows. Installed databases retain current values, while new device rows use the table default.

- [ ] **Step 4: Run the focused test and verify GREEN**

Run: `go test ./internal/store -run TestDeviceKafkaConfigDefaultsUpdatesAndCascades -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the default-value change**

```powershell
git add internal/store/store_test.go internal/store/database.go
git commit -m "fix: default Kafka full interval to one second"
```

### Task 2: Apply Compact Kafka Form Layouts

**Files:**
- Modify: `web/embed_test.go:170`
- Modify: `web/index.html:11`
- Modify: `web/device.html:8`
- Modify: `web/css/app.css:1`

**Interfaces:**
- Consumes: existing element IDs used by `web/js/devices.js` and `web/js/device.js`.
- Produces: CSS hooks `kafka-form-grid`, `full-row`, `inline-check`, and `kafka-mode-row`; element IDs and form submission behavior remain unchanged.

- [ ] **Step 1: Add failing embedded-asset assertions**

Replace the Kafka HTML entries in `TestKafkaManagementAssets` with:

```go
checks := map[string][]string{
	"index.html": {`id="kafka-form" class="kafka-form-grid"`, `class="inline-check full-row"`, `class="actions dialog-actions full-row"`},
	"device.html": {`id="device-kafka-form"`, `class="inline-check"`, `class="kafka-mode-row"`, `id="device-kafka-interval" type="number" min="1" max="86400" value="1"`},
	"css/app.css": {`.kafka-form-grid`, `.inline-check`, `.kafka-mode-row`},
	"js/devices.js": {`/api/v1/kafka`, `clear_sasl_password`},
	"js/device.js": {`/api/v1/devices/${id}/kafka`, `input[name="kafka-mode"]:checked`},
}
```

- [ ] **Step 2: Run the focused asset test and verify RED**

Run: `go test ./web -run TestKafkaManagementAssets -count=1`

Expected: FAIL listing missing layout classes and the missing `value="1"` marker.

- [ ] **Step 3: Mark up the global Kafka form**

Set the form opening tag and full-row elements as follows, while keeping every existing field ID and option value:

```html
<form id="kafka-form" class="kafka-form-grid">
<h2 class="full-row">Kafka 设置</h2>
<p class="full-row">状态：<span id="kafka-state" class="badge">disabled</span></p>
<p id="kafka-error" class="full-row"></p>
<label class="inline-check full-row">启用<input id="kafka-enabled" type="checkbox"></label>
<div class="actions dialog-actions full-row"><button type="button" data-kafka-close>取消</button><button type="submit">保存</button></div>
```

The remaining labels are automatic two-column grid items.

- [ ] **Step 4: Mark up the device Kafka form**

Use this structure while preserving the current IDs and radio values:

```html
<form id="device-kafka-form">
  <h2>Kafka 推送</h2>
  <label class="inline-check">启用<input id="device-kafka-enabled" type="checkbox"></label>
  <label>Topic<input id="device-kafka-topic" maxlength="249"></label>
  <fieldset class="kafka-mode-row">
    <legend>推送模式</legend>
    <label><input type="radio" name="kafka-mode" value="change" checked>变化推送</label>
    <label><input type="radio" name="kafka-mode" value="full">全量推送</label>
  </fieldset>
  <label>全量周期（秒）<input id="device-kafka-interval" type="number" min="1" max="86400" value="1"></label>
  <div class="actions"><button type="submit">保存 Kafka 配置</button></div>
</form>
```

- [ ] **Step 5: Add the responsive presentation rules**

Add alongside the existing form rules:

```css
.kafka-form-grid{grid-template-columns:minmax(0,1fr) minmax(0,1fr)}
.kafka-form-grid .full-row{grid-column:1/-1}
.inline-check{display:flex;align-items:center;gap:.5rem}
.inline-check input{margin:0}
.kafka-mode-row{display:flex;align-items:center;gap:1rem;margin:0;padding:.75rem;border:1px solid #d1d5db;border-radius:.35rem}
.kafka-mode-row label{display:flex;grid-template-columns:auto auto;align-items:center;gap:.4rem}
.kafka-mode-row input{margin:0}
```

Extend the existing `@media(max-width:700px)` block with:

```css
.kafka-form-grid{grid-template-columns:1fr}
```

- [ ] **Step 6: Run the focused test and verify GREEN**

Run: `go test ./web -run TestKafkaManagementAssets -count=1`

Expected: PASS.

- [ ] **Step 7: Run formatting and full verification**

```powershell
gofmt -w web/embed_test.go internal/store/store_test.go internal/store/database.go
go test ./...
go test -race ./...
go vet ./...
```

Expected: every command exits with code 0 and all Go package tests pass.

- [ ] **Step 8: Commit the form layout change**

```powershell
git add web/embed_test.go web/index.html web/device.html web/css/app.css
git commit -m "feat: compact Kafka configuration forms"
```
