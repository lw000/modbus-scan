package web

import (
	"io/fs"
	"strings"
	"testing"
)

func TestAssetsContainEntryPages(t *testing.T) {
	for _, name := range []string{"index.html", "device.html", "css/app.css", "js/api.js"} {
		if _, err := fs.Stat(Assets, name); err != nil {
			t.Fatalf("asset %s: %v", name, err)
		}
	}
}

func TestDevicePageContainsPaginatedPointTable(t *testing.T) {
	data, err := fs.ReadFile(Assets, "device.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, required := range []string{
		`id="points-table"`,
		`class="table-scroll"`,
		`data-column="bit-offset"`,
		`data-column="bit-len"`,
		`data-column="description"`,
		`class="column-resizer"`,
		`id="description"`,
		`maxlength="255"`,
		`class="form-row two-columns"`,
		`class="writeable-field"`,
		`class="actions dialog-actions"`,
		`class="sticky-actions"`,
		`id="page-size"`,
		`value="500"`,
		`id="prev-page"`,
		`id="next-page"`,
		`id="delete-page"`,
		`id="realtime-dialog"`,
		`id="realtime-chart"`,
		`id="realtime-window"`,
		`id="realtime-decimal"`,
		`id="realtime-binary"`,
		`id="realtime-hexadecimal"`,
		`value="1"`,
		`value="5" selected`,
		`value="10"`,
		`value="30"`,
		`时间窗口`,
		`>读写<`,
	} {
		if !strings.Contains(page, required) {
			t.Errorf("device.html missing %q", required)
		}
	}
	if strings.Contains(page, `id="values"`) {
		t.Error("device.html still contains the separate values table")
	}
}

func TestDeviceScriptContainsPointTableBehavior(t *testing.T) {
	data, err := fs.ReadFile(Assets, "js/device.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	pageData, err := fs.ReadFile(Assets, "device.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(pageData)
	for _, required := range []string{
		"modbus-scan.point-column-widths.v1",
		"limit=${pageState.size}",
		"offset=${offset}",
		"initColumnResizing",
		"esc(point.description)",
		"point.writeable === 1",
		"checked ? 1 : 0",
		"openRealtime",
		"CHART_WINDOW_MINUTES",
		"MAX_CHART_AGE_MS",
		"realtimeWindowMinutes",
		"drawChartAxes",
		"devicePixelRatio",
		"realtimeRedrawTimer",
		"WebSocket.OPEN",
		"window.addEventListener(\"resize\"",
		"pageItems.map",
		"formatRealtimeFormats",
		`new Set(["Int16", "UInt16", "Int32", "UInt32"])`,
		`formatRadix(integer, 2, "0b")`,
		`formatRadix(integer, 16, "0x")`,
		"realtimeLatestValue",
		"function sampleRealtimeValue()",
		"Math.floor(Date.now() / 1000) * 1000",
		"function formatChartTime(at)",
		"setInterval(sampleRealtimeValue, 1000)",
		`addEventListener("pointermove"`,
		`addEventListener("pointerleave"`,
		"function drawChartTooltip",
		"realtimeHoverX",
		"ctx.setLineDash([4, 4])",
		"Math.abs(xFor(sample.at) - realtimeHoverX)",
		"realtimeLatestValue !== null && latest.segment === realtimeSegment) ctx.lineTo",
		"function loadStatus()",
		"`/api/v1/devices/${id}/status`",
		"loadStatus().catch(showError)",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("device.js missing %q", required)
		}
	}
	for _, removed := range []string{`data-column="value"`, `data-column="updated-at"`, `data-live-value`, `data-live-updated`, `function loadValues`, `function applyValues`} {
		if strings.Contains(page+script, removed) {
			t.Errorf("point list still contains removed behavior %q", removed)
		}
	}
	if strings.Contains(script, "loadSummary().catch(showError)") {
		t.Error("device.js still polls the full device summary")
	}
	if strings.Contains(script, "`${seconds}s`") {
		t.Error("device.js still renders relative-second X-axis labels")
	}
	cssData, err := fs.ReadFile(Assets, "css/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssData)
	for _, required := range []string{".sticky-actions", ".dialog-actions", ".two-columns", ".realtime-chart-toolbar", ".realtime-formats"} {
		if !strings.Contains(css, required) {
			t.Errorf("app.css missing %q", required)
		}
	}
}
