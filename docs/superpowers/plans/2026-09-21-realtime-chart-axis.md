# Realtime Chart Axes and Time Windows Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add readable axes and 1/5/10/30-minute rolling windows to the point realtime chart, with a 5-minute default and horizontal rendering for unchanged values.

**Architecture:** Keep the existing WebSocket protocol and vanilla Canvas chart. The browser stores timestamped numeric samples for at most 30 minutes, derives the selected visible window at draw time, and maps real timestamps and values into a padded plot rectangle. Connection-aware periodic redraw extends the last confirmed value only while the socket is open.

**Tech Stack:** Go 1.21 embedded-resource tests, HTML, CSS, vanilla JavaScript, Canvas 2D API, browser WebSocket API.

## Global Constraints

- Do not add a charting library or any other dependency.
- Do not change backend collection, persistence, or the WebSocket wire protocol.
- Time-window options are exactly 1, 5, 10, and 30 minutes; default is 5 minutes.
- The X axis is expressed in seconds from the current time, ending at `0s`.
- Retain no more than 30 minutes of chart samples in browser memory.
- Extend the last value horizontally only while WebSocket state is `OPEN`; never fill a disconnected interval.
- Preserve the existing realtime dialog session guard and cleanup behavior.

---

## File Structure

- `web/embed_test.go`: embedded HTML, JavaScript, and CSS contract tests for the chart controls and behavior markers.
- `web/device.html`: accessible time-window selector adjacent to the realtime reading.
- `web/js/device.js`: timestamped sample retention, window selection, axis layout, Canvas drawing, socket-aware line extension, redraw lifecycle, and cleanup.
- `web/css/app.css`: compact chart toolbar and responsive selector layout.

### Task 1: Define the Embedded UI and Script Contract

**Files:**
- Modify: `web/embed_test.go`

**Interfaces:**
- Consumes: embedded assets exposed by `web.Assets`.
- Produces: contract requirements for `#realtime-window`, four exact minute values, chart-axis drawing helpers, and redraw cleanup markers.

- [ ] **Step 1: Add failing HTML and JavaScript contract assertions**

Extend `TestDevicePageContainsPaginatedPointTable` with these required HTML fragments:

```go
for _, required := range []string{
	`id="realtime-window"`,
	`value="1"`,
	`value="5" selected`,
	`value="10"`,
	`value="30"`,
	`时间窗口`,
} {
	if !strings.Contains(page, required) {
		t.Errorf("device.html missing %q", required)
	}
}
```

Extend `TestDeviceScriptContainsPointTableBehavior` with exact behavior markers:

```go
for _, required := range []string{
	"CHART_WINDOW_MINUTES",
	"MAX_CHART_AGE_MS",
	"realtimeWindowMinutes",
	"drawChartAxes",
	"devicePixelRatio",
	"realtimeRedrawTimer",
	"WebSocket.OPEN",
	"window.addEventListener(\"resize\"",
} {
	if !strings.Contains(script, required) {
		t.Errorf("device.js missing %q", required)
	}
}
```

In the existing required-marker list, replace `"MAX_CHART_SAMPLES"` with `"MAX_CHART_AGE_MS"`; do not require both the removed count limit and the new time limit.

Extend the CSS fragment list with `".realtime-chart-toolbar"`.

- [ ] **Step 2: Run the focused test and verify RED**

Run: `go test ./web -run 'TestDevicePageContainsPaginatedPointTable|TestDeviceScriptContainsPointTableBehavior' -count=1`

Expected: FAIL because the selector, chart constants, axis helper, redraw timer, and toolbar style do not yet exist.

- [ ] **Step 3: Commit the failing contract test**

```powershell
git add web/embed_test.go
git commit -m "test: define realtime chart axis contract"
```

### Task 2: Implement Rolling Time-Axis Chart Rendering

**Files:**
- Modify: `web/device.html`
- Modify: `web/js/device.js`
- Modify: `web/css/app.css`
- Test: `web/embed_test.go`

**Interfaces:**
- Consumes: WebSocket value messages shaped as `{type, device_id, tag_name, value, updated_at}` and the existing realtime dialog lifecycle.
- Produces: `CHART_WINDOW_MINUTES`, `MAX_CHART_AGE_MS`, `realtimeWindowMinutes`, `drawChartAxes(ctx, plot, min, max, windowSeconds)`, and `drawRealtimeChart()`.

- [ ] **Step 1: Add the time-window selector markup**

Replace the realtime dialog body around the reading and canvas with:

```html
<div class="realtime-reading"><strong id="realtime-value">--</strong><span id="realtime-updated">--</span><span id="realtime-status">连接中</span></div>
<div class="realtime-chart-toolbar"><label for="realtime-window">时间窗口</label><select id="realtime-window"><option value="1">1 分钟</option><option value="5" selected>5 分钟</option><option value="10">10 分钟</option><option value="30">30 分钟</option></select></div>
<canvas id="realtime-chart" width="800" height="320"></canvas>
```

Do not change the dialog IDs, close button, or surrounding section.

- [ ] **Step 2: Replace count-based chart state with bounded timestamp state**

At the top of `web/js/device.js`, replace `MAX_CHART_SAMPLES` and extend realtime state with:

```javascript
const CHART_WINDOW_MINUTES = Object.freeze([1, 5, 10, 30]);
const DEFAULT_CHART_WINDOW_MINUTES = 5;
const MAX_CHART_AGE_MS = 30 * 60 * 1000;
let realtimeWindowMinutes = DEFAULT_CHART_WINDOW_MINUTES;
let realtimeRedrawTimer = null;
let realtimeConnected = false;
```

Store samples as `{value: numeric, at: timestampMilliseconds}`. On each accepted sample, discard entries older than `Date.now() - MAX_CHART_AGE_MS`, append the new sample, and sort ascending by `at` so an out-of-order message cannot fold the line backward. Reject non-finite parsed timestamps and numeric values.

- [ ] **Step 3: Implement device-pixel-ratio sizing and coordinate helpers**

Add a helper that reads `canvas.getBoundingClientRect()`, uses a minimum logical width and height when the browser reports zero, sets `canvas.width` and `canvas.height` to logical dimensions multiplied by `window.devicePixelRatio || 1`, then calls `ctx.setTransform(ratio, 0, 0, ratio, 0, 0)`.

Use this plot rectangle in logical pixels:

```javascript
const plot = {
  left: 64,
  top: 16,
  right: width - 16,
  bottom: height - 40,
};
```

Map X from `[now - windowMilliseconds, now]` into `[plot.left, plot.right]`, clamping future timestamps to `now`. Map Y from `[min, max]` into `[plot.bottom, plot.top]`.

- [ ] **Step 4: Draw axes, grid, and labels**

Implement `drawChartAxes(ctx, plot, min, max, windowSeconds)` with five equal X intervals and four equal Y intervals. Draw grid lines in `#e5e7eb`, axes in `#6b7280`, and labels in `12px system-ui` using `#4b5563`.

X labels must be integer seconds from `-windowSeconds` through `0s`. Y labels must use a compact formatter: integers without decimals, magnitudes below 10 with up to two decimals, and other fractional values with up to one decimal. Keep all labels inside the Canvas margins.

- [ ] **Step 5: Draw visible samples and unchanged-value extension**

Rewrite `drawRealtimeChart()` to:

1. Determine the selected duration from `realtimeWindowMinutes`, falling back to 5 when it is not in `CHART_WINDOW_MINUTES`.
2. Filter samples at or after `now - duration`.
3. Derive Y bounds from visible values. If none exist, use `0..1`. If all values are equal, use padding `Math.max(Math.abs(value) * 0.05, 1)` above and below.
4. Draw axes before data.
5. Draw the blue line using actual timestamps. Draw a 3-pixel point when only one sample is visible.
6. When `realtimeConnected` is true and `realtimeSocket?.readyState === WebSocket.OPEN`, add a final horizontal segment from the latest visible sample to `now`. When disconnected, stop at the sample timestamp.

Do not synthesize or insert the extended endpoint into `realtimeSamples`; it is a rendering-only point.

- [ ] **Step 6: Wire selection, socket status, timers, and cleanup**

Bind `#realtime-window` change events to parse the minute value, validate membership in `CHART_WINDOW_MINUTES`, assign the default on invalid input, and redraw immediately.

In `openRealtime`, reset the selector and `realtimeWindowMinutes` to 5. Set `realtimeConnected = true` in the guarded `socket.onopen`; set it to false and redraw before scheduling reconnect in the guarded `socket.onclose`. After opening the dialog, start one `setInterval(drawRealtimeChart, 1000)` timer.

In `closeRealtime`, set `realtimeConnected = false`, call `clearInterval(realtimeRedrawTimer)`, assign `null`, and preserve the current WebSocket, reconnect-timer, session, sample, and dialog cleanup.

Add:

```javascript
window.addEventListener("resize", () => {
  if (realtimeDialog.open) drawRealtimeChart();
});
```

- [ ] **Step 7: Add focused responsive styles**

Append styles equivalent to:

```css
.realtime-chart-toolbar{display:flex;justify-content:flex-end;align-items:center;gap:.5rem}.realtime-chart-toolbar label{display:flex;grid-template-columns:auto auto;align-items:center;gap:.5rem}.realtime-chart-toolbar select{min-width:7rem}@media(max-width:700px){.realtime-chart-toolbar{justify-content:flex-start}}
```

Keep the existing chart height, border, background, and responsive dialog width.

- [ ] **Step 8: Run the web tests and verify GREEN**

Run: `go test ./web -count=1`

Expected: PASS.

- [ ] **Step 9: Inspect the diff for accidental protocol or backend changes**

Run: `git diff --check`

Run: `git diff -- web/device.html web/js/device.js web/css/app.css web/embed_test.go`

Expected: no whitespace errors; changes are limited to the selector, Canvas rendering/lifecycle, styles, and asset contract tests.

- [ ] **Step 10: Commit the chart implementation**

```powershell
git add web/device.html web/js/device.js web/css/app.css
git commit -m "feat: add realtime chart axes and windows"
```

### Task 3: Full Verification and Browser Acceptance

**Files:**
- Verify: `web/device.html`
- Verify: `web/js/device.js`
- Verify: `web/css/app.css`
- Verify: `web/embed_test.go`

**Interfaces:**
- Consumes: completed browser chart implementation.
- Produces: verified embedded assets and a recorded manual acceptance result.

- [ ] **Step 1: Run the full Go test suite**

Run: `go test ./... -count=1`

Expected: PASS for all packages.

- [ ] **Step 2: Run static analysis**

Run: `go vet ./...`

Expected: exit code 0 with no diagnostics.

- [ ] **Step 3: Perform browser acceptance checks**

Start the existing service with its normal local configuration and verify all of the following:

```text
1. Opening a point shows axes immediately and defaults to 5 minutes.
2. The selector switches among 1, 5, 10, and 30 minutes without clearing retained samples.
3. The X labels use seconds and end at 0s; Y labels show point values.
4. Unevenly timed samples have uneven horizontal spacing.
5. A stable value is a horizontal line extended to the current time while connected.
6. Disconnecting stops the extension at the last confirmed sample; reconnecting resumes only from a new sample.
7. Empty, one-sample, constant, changing, and Bool series remain legible.
8. Resizing the window and using a high-DPI display keeps lines and text sharp.
9. Closing and reopening the dialog clears the old point samples and does not leave redraw timers running.
```

- [ ] **Step 4: Review repository state**

Run: `git status --short`

Expected: no generated binaries, logs, databases, caches, or test artifacts were added; pre-existing unrelated user changes remain untouched.
