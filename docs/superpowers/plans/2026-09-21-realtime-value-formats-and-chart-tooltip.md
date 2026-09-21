# Realtime Value Formats and Chart Tooltip Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Display decimal, binary, and hexadecimal realtime values and provide a one-sample-per-second scrolling chart with hover readouts.

**Architecture:** Keep WebSocket messages as the latest-value source and move chart insertion to the existing one-second timer. Extend the native Canvas renderer with local-time ticks, nearest-sample hit testing, and an in-canvas tooltip.

**Tech Stack:** Embedded HTML/CSS, vanilla JavaScript, Canvas 2D, Go `embed.FS`, Go `testing`.

## Global Constraints

- Do not change backend APIs, collection logic, WebSocket protocol, or persistence.
- Do not add third-party frontend dependencies.
- Do not create samples while disconnected or connect lines across reconnect gaps.
- Keep the existing 1, 5, 10, and 30 minute windows.

---

### Task 1: Multi-format Realtime Reading

**Files:**
- Modify: `web/device.html`
- Modify: `web/js/device.js`
- Modify: `web/css/app.css`
- Test: `web/embed_test.go`

**Interfaces:**
- Consumes: the active point's `data_type` and WebSocket `message.value`.
- Produces: `formatRealtimeFormats(value, dataType)` returning `{decimal, binary, hexadecimal}` and DOM fields `realtime-decimal`, `realtime-binary`, and `realtime-hexadecimal`.

- [x] **Step 1: Add failing embedded-resource assertions**

Require the three DOM IDs, `formatRealtimeFormats`, integer type recognition, radix conversion with `0b`/`0x`, and `.realtime-formats` CSS.

- [x] **Step 2: Verify RED**

Run: `go test ./web -run 'TestDevice(Page|Script)Contains' -count=1`

Expected: FAIL because the new fields and formatter are absent.

- [x] **Step 3: Implement the reading UI and formatter**

Replace the single realtime value with labeled format rows. Map Bool to 0/1; map Int16/UInt16/Int32/UInt32 with signed magnitude radix strings; return `—` for float radix values and invalid values. Reset and update all fields in `openRealtime` and `socket.onmessage`.

- [x] **Step 4: Verify GREEN**

Run: `go test ./web -run 'TestDevice(Page|Script)Contains' -count=1`

Expected: PASS.

### Task 2: One-second Sampling and Absolute Time Axis

**Files:**
- Modify: `web/js/device.js`
- Test: `web/embed_test.go`

**Interfaces:**
- Consumes: `realtimeLatestValue`, `realtimeConnected`, `realtimeSegment`, and `Date.now()`.
- Produces: `sampleRealtimeValue()` and `formatChartTime(at)`.

- [x] **Step 1: Add failing behavior assertions**

Require latest-value state, `sampleRealtimeValue`, natural-second deduplication, `formatChartTime`, and `setInterval(sampleRealtimeValue, 1000)`. Reject direct `realtimeSamples.push` inside the WebSocket message block.

- [x] **Step 2: Verify RED**

Run: `go test ./web -run TestDeviceScriptContainsPointTableBehavior -count=1`

Expected: FAIL because messages currently append samples and ticks are relative seconds.

- [x] **Step 3: Implement sampling and time ticks**

Store only the latest finite numeric value on each message. Once per second, append or replace the current natural-second sample while connected, prune samples older than 30 minutes, and redraw. Render X-axis labels with local `HH:mm:ss` values derived from each tick timestamp.

- [x] **Step 4: Verify GREEN**

Run: `go test ./web -run TestDeviceScriptContainsPointTableBehavior -count=1`

Expected: PASS.

### Task 3: Canvas Hover Readout

**Files:**
- Modify: `web/js/device.js`
- Test: `web/embed_test.go`

**Interfaces:**
- Consumes: pointer X coordinate, visible real samples, and chart coordinate functions.
- Produces: `realtimeHoverX`, `drawChartTooltip`, and Canvas `pointermove`/`pointerleave` handlers.

- [x] **Step 1: Add failing hover assertions**

Require `pointermove`, `pointerleave`, `drawChartTooltip`, nearest-sample selection, `ctx.setLineDash`, and tooltip time/value text.

- [x] **Step 2: Verify RED**

Run: `go test ./web -run TestDeviceScriptContainsPointTableBehavior -count=1`

Expected: FAIL because hover behavior is absent.

- [x] **Step 3: Implement hover interaction**

Track the pointer's Canvas-relative X position, choose the visible real sample whose rendered X is nearest, draw a dashed vertical guide, highlighted point, and bounded tooltip containing `formatChartTime(sample.at)` and `formatChartValue(sample.value)`. Clear hover state on leave and dialog cleanup.

- [x] **Step 4: Run final verification**

Run: `go test ./web -count=1`

Expected: PASS.

Run: `go test ./... -count=1`

Expected: PASS.
