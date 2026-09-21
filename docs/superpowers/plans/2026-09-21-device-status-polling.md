# Device Status Polling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop polling the full device-detail endpoint and poll only the existing device-status endpoint while the detail page is visible.

**Architecture:** Keep the initial full-device request for static fields. Extract runtime summary rendering so the initial response and later `/status` responses share it, then make the two-second timer call only the status loader.

**Tech Stack:** Embedded vanilla JavaScript, Go `embed.FS`, Go `testing`.

## Global Constraints

- Do not change backend APIs, storage, collector runtime, or the WebSocket protocol.
- Do not restore HTTP polling of point values.
- Preserve the two-second visible-page polling interval.

---

### Task 1: Poll Device Runtime Status Only

**Files:**
- Modify: `web/embed_test.go`
- Modify: `web/js/device.js`

**Interfaces:**
- Consumes: `GET /api/v1/devices/:id/status`, returning the existing runtime snapshot object.
- Produces: `renderSummary(device, runtime)` and `loadStatus()` browser functions.

- [x] **Step 1: Write the failing resource test**

Extend `TestDeviceScriptContainsPointTableBehavior` to require `function loadStatus()`, the `/status` request, and `loadStatus().catch(showError)`. Reject `loadSummary().catch(showError)` so the timer cannot poll the full endpoint.

- [x] **Step 2: Run the focused test and verify RED**

Run: `go test ./web -run TestDeviceScriptContainsPointTableBehavior -count=1`

Expected: FAIL because `loadStatus` and the `/status` request are absent and the timer still calls `loadSummary`.

- [x] **Step 3: Implement the minimal browser change**

In `web/js/device.js`, extract summary DOM rendering into `renderSummary(device, runtime)`. Keep `loadSummary()` requesting `/api/v1/devices/${id}` and pass its two response fields to the renderer. Add `loadStatus()` requesting `/api/v1/devices/${id}/status` and render it with the current device data retained from the initial load. Change the two-second timer to call `loadStatus()` only while the document is visible.

- [x] **Step 4: Run focused and full verification**

Run: `go test ./web -count=1`

Expected: PASS.

Run: `go test ./...`

Expected: PASS.
