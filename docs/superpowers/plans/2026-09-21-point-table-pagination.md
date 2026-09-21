# Point Table Pagination and Live Values Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add resizable persistent columns, a vertically scrollable paginated point table, and current-page live values to the device detail page.

**Architecture:** Keep point configuration paging in the existing SQLite-backed points endpoint and raise its bounded page size to 500. Keep runtime values on the existing values endpoint; the browser stores the latest snapshot and patches only rows rendered for the current point page. Native HTML, CSS, and JavaScript provide table layout, pagination, request sequencing, and `localStorage` column widths.

**Tech Stack:** Go 1.24, Gin, `net/http/httptest`, embedded HTML/CSS/JavaScript, browser Fetch API and `localStorage`.

## Global Constraints

- Do not add frontend or Go dependencies.
- Preserve the existing `/api/v1/devices/:id/points` response shape: `{items, total}`.
- Accept point page limits from 1 through 500; reject all other values.
- Offer page sizes exactly `10, 20, 50, 100, 200, 500`, with 20 selected by default.
- Poll device summary and runtime values every 2 seconds; do not poll point configuration.
- Persist valid column widths in `localStorage`; storage failures must not break the table.
- Escape all external values before inserting them into HTML.
- Preserve unrelated user changes in the dirty worktree.

## File Structure

- Modify `internal/httpapi/point_handler.go`: validate the expanded page-size boundary.
- Modify `internal/httpapi/httpapi_test.go`: exercise accepted/rejected pagination and forwarded filters.
- Modify `web/device.html`: make one point table contain configuration and live-value columns, and add pagination controls.
- Modify `web/css/app.css`: add scroll container, sticky header, resizer, fixed-layout columns, and pagination styling.
- Modify `web/js/device.js`: own page/filter/request state, render current-page points, patch live values, and persist column widths.
- Modify `web/embed_test.go`: verify the embedded device page includes the required structural hooks and assets.

---

### Task 1: Expand the Point API Page Limit

**Files:**
- Modify: `internal/httpapi/httpapi_test.go`
- Modify: `internal/httpapi/point_handler.go`

**Interfaces:**
- Consumes: `GET /api/v1/devices/:id/points?limit=<n>&offset=<n>&search=<text>&reg_type=<type>`.
- Produces: a successful response for `limit=500`; `400 invalid_pagination` for `limit=501`, zero, negative, or non-numeric values; an unchanged `store.PointFilter` passed into `PointService.List`.

- [ ] **Step 1: Make the point service stub capture the requested filter**

Replace the stub definition and `List` method with:

```go
type stubPoints struct {
	imported  string
	lastFilter store.PointFilter
}

func (s *stubPoints) List(_ context.Context, _ int64, filter store.PointFilter) (store.PointPage, error) {
	s.lastFilter = filter
	return store.PointPage{Items: []model.Point{}, Total: 123}, nil
}
```

- [ ] **Step 2: Write failing table-driven handler tests**

Append this test to `internal/httpapi/httpapi_test.go`:

```go
func TestListPointsPagination(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantLimit  int
	}{
		{name: "maximum", query: "limit=500&offset=20&search=temp&reg_type=HoldingReg", wantStatus: http.StatusOK, wantLimit: 500},
		{name: "over maximum", query: "limit=501", wantStatus: http.StatusBadRequest},
		{name: "zero", query: "limit=0", wantStatus: http.StatusBadRequest},
		{name: "negative", query: "limit=-1", wantStatus: http.StatusBadRequest},
		{name: "not a number", query: "limit=many", wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			points := &stubPoints{}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/1/points?"+tt.query, nil)
			rec := httptest.NewRecorder()
			testRouter(&stubDevices{}, points).ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if tt.wantStatus != http.StatusOK {
				if !strings.Contains(rec.Body.String(), `"code":"invalid_pagination"`) {
					t.Fatalf("body=%s", rec.Body.String())
				}
				return
			}
			if points.lastFilter.Limit != tt.wantLimit || points.lastFilter.Offset != 20 || points.lastFilter.Search != "temp" || points.lastFilter.RegType != "HoldingReg" {
				t.Fatalf("filter=%+v", points.lastFilter)
			}
		})
	}
}
```

- [ ] **Step 3: Run the focused test and verify the maximum case fails**

Run: `go test ./internal/httpapi -run TestListPointsPagination -count=1`

Expected: FAIL because `limit=500` currently returns status 400.

- [ ] **Step 4: Raise the bounded maximum and update the client-safe error**

In `internal/httpapi/point_handler.go`, change the limit validation to:

```go
if err != nil || limit < 1 || limit > 500 {
	failure(c, http.StatusBadRequest, "invalid_pagination", "limit 必须为 1 到 500", nil)
	return
}
```

- [ ] **Step 5: Format and rerun the focused package**

Run: `gofmt -w internal/httpapi/point_handler.go internal/httpapi/httpapi_test.go`

Run: `go test ./internal/httpapi -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the bounded API change**

```powershell
git add internal/httpapi/point_handler.go internal/httpapi/httpapi_test.go
git commit -m "feat: allow 500 point page size"
```

---

### Task 2: Build the Scrollable Resizable Table Shell

**Files:**
- Modify: `web/embed_test.go`
- Modify: `web/device.html`
- Modify: `web/css/app.css`

**Interfaces:**
- Consumes: rows rendered into `#points` by Task 3.
- Produces: `#points-table`, a `colgroup` with `data-column` keys, `.column-resizer` handles, `#page-size`, `#page-status`, `#prev-page`, and `#next-page` hooks.

- [ ] **Step 1: Add a failing embedded-markup test**

Add `strings` to the imports and append:

```go
func TestDevicePageContainsPaginatedPointTable(t *testing.T) {
	data, err := fs.ReadFile(Assets, "device.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, required := range []string{
		`id="points-table"`,
		`class="table-scroll"`,
		`data-column="value"`,
		`data-column="updated-at"`,
		`class="column-resizer"`,
		`id="page-size"`,
		`value="500"`,
		`id="prev-page"`,
		`id="next-page"`,
	} {
		if !strings.Contains(page, required) {
			t.Errorf("device.html missing %q", required)
		}
	}
	if strings.Contains(page, `id="values"`) {
		t.Error("device.html still contains the separate values table")
	}
}
```

- [ ] **Step 2: Run the embedded asset test and verify it fails**

Run: `go test ./web -run TestDevicePageContainsPaginatedPointTable -count=1`

Expected: FAIL with missing `id="points-table"` and the old `id="values"` still present.

- [ ] **Step 3: Replace the point table and remove the separate value section**

In `web/device.html`, keep the existing toolbar, then replace its table and the following real-time-value section with this structure (use the page's existing Chinese labels):

```html
<div class="table-scroll">
  <table id="points-table">
    <colgroup>
      <col data-column="tag-name"><col data-column="reg-type"><col data-column="address">
      <col data-column="data-type"><col data-column="bits"><col data-column="writeable">
      <col data-column="value"><col data-column="updated-at"><col data-column="actions">
    </colgroup>
    <thead><tr>
      <th>TagName<span class="column-resizer" data-resize="tag-name"></span></th>
      <th>类型<span class="column-resizer" data-resize="reg-type"></span></th>
      <th>地址<span class="column-resizer" data-resize="address"></span></th>
      <th>数据类型<span class="column-resizer" data-resize="data-type"></span></th>
      <th>位<span class="column-resizer" data-resize="bits"></span></th>
      <th>可写<span class="column-resizer" data-resize="writeable"></span></th>
      <th>实时值<span class="column-resizer" data-resize="value"></span></th>
      <th>更新时间<span class="column-resizer" data-resize="updated-at"></span></th>
      <th>操作<span class="column-resizer" data-resize="actions"></span></th>
    </tr></thead>
    <tbody id="points"></tbody>
  </table>
</div>
<div class="pagination">
  <label>每页<select id="page-size"><option>10</option><option selected>20</option><option>50</option><option>100</option><option>200</option><option value="500">500</option></select></label>
  <button id="prev-page" type="button">上一页</button>
  <span id="page-status" aria-live="polite"></span>
  <button id="next-page" type="button">下一页</button>
</div>
```

- [ ] **Step 4: Add focused table and pagination styles**

Append these rules to `web/css/app.css`, retaining existing shared styles:

```css
.table-scroll{max-height:60vh;overflow:auto;border:1px solid #e5e7eb;border-radius:.35rem}
#points-table{table-layout:fixed;min-width:72rem}
#points-table th{position:sticky;top:0;z-index:1;background:#fff}
#points-table th,#points-table td{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
#points-table th{position:sticky}
.column-resizer{position:absolute;top:0;right:-3px;width:7px;height:100%;cursor:col-resize;user-select:none;touch-action:none}
.column-resizer::after{content:"";position:absolute;top:20%;bottom:20%;left:3px;border-left:1px solid #9ca3af}
body.resizing-column{cursor:col-resize;user-select:none}
.pagination{display:flex;justify-content:flex-end;align-items:center;gap:.5rem;margin-top:.75rem;flex-wrap:wrap}
.pagination label{display:flex;grid-template-columns:auto auto;align-items:center;gap:.4rem}
.pagination button:disabled{background:#9ca3af}
```

Update the existing narrow-screen rule so it no longer hides the fifth point-table column; horizontal scrolling must preserve all nine columns.

- [ ] **Step 5: Run asset tests**

Run: `go test ./web -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the table shell**

```powershell
git add web/device.html web/css/app.css web/embed_test.go
git commit -m "feat: add scrollable point table shell"
```

---

### Task 3: Implement Paging, Current-Page Values, and Persistent Widths

**Files:**
- Modify: `web/js/device.js`
- Modify: `web/embed_test.go`

**Interfaces:**
- Consumes: `api.get()` responses shaped as `{items: Point[], total: number}` and `{[tagName]: {value: any, updated_at: string}}`; Task 2 DOM hooks.
- Produces: `loadPoints()`, `loadValues()`, `renderPoints()`, `applyValues()`, `setPage()`, and `initColumnResizing()` browser functions; page state `{number, size, total}`; local-storage key `modbus-scan.point-column-widths.v1`.

- [ ] **Step 1: Extend the static asset test with behavior markers**

Append to `web/embed_test.go`:

```go
func TestDeviceScriptContainsPointTableBehavior(t *testing.T) {
	data, err := fs.ReadFile(Assets, "js/device.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{
		"modbus-scan.point-column-widths.v1",
		"limit=${pageState.size}",
		"offset=${offset}",
		"data-tag=",
		"applyValues",
		"initColumnResizing",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("device.js missing %q", required)
		}
	}
}
```

- [ ] **Step 2: Run the behavior-marker test and verify it fails**

Run: `go test ./web -run TestDeviceScriptContainsPointTableBehavior -count=1`

Expected: FAIL because the existing script has none of the new state or resizing markers.

- [ ] **Step 3: Introduce point-page state and safe request sequencing**

At the top of `web/js/device.js`, remove `valuesBody`, keep the existing element references, and add:

```js
const pageSize=document.querySelector("#page-size"),pageStatus=document.querySelector("#page-status"),prevPage=document.querySelector("#prev-page"),nextPage=document.querySelector("#next-page");
const pageState={number:1,size:20,total:0};
const columnStorageKey="modbus-scan.point-column-widths.v1";
let busy=false,pointsRequest=0,latestValues={};
```

Implement the helpers:

```js
const totalPages=()=>Math.max(1,Math.ceil(pageState.total/pageState.size));
function updatePager(){pageStatus.textContent=`第 ${pageState.number} / ${totalPages()} 页，共 ${pageState.total} 条`;prevPage.disabled=pageState.number<=1;nextPage.disabled=pageState.number>=totalPages()}
function setPage(number){const next=Math.min(Math.max(1,number),totalPages());if(next===pageState.number)return;pageState.number=next;loadPoints().catch(showError)}
```

Use a monotonically increasing `pointsRequest` in `loadPoints`: calculate `offset=(pageState.number-1)*pageState.size`, request `limit=${pageState.size}&offset=${offset}`, and ignore the response unless its request number still equals `pointsRequest`. After assigning `pageState.total`, if the requested page is beyond `totalPages()`, set the page number to the last page and request once more. Otherwise call `renderPoints(page.items||[])` and `updatePager()`.

- [ ] **Step 4: Render live-value cells inside point rows**

Define the row renderer and value patcher with encoded TagName attributes:

```js
function renderPoints(points){pointsBody.innerHTML=points.map(p=>`<tr data-tag="${esc(p.tag_name)}"><td>${esc(p.tag_name)}</td><td>${esc(p.reg_type)}</td><td>${p.address}</td><td>${esc(p.data_type)}</td><td>${p.bit_offset}/${p.bit_len}</td><td>${p.writeable}</td><td data-live-value>--</td><td data-live-updated>--</td><td><button data-edit='${JSON.stringify(p).replace(/'/g,"&#39;")}'>编辑</button> <button class="danger" data-delete="${p.id}">删除</button></td></tr>`).join("");applyValues(latestValues)}
function applyValues(values){pointsBody.querySelectorAll("tr[data-tag]").forEach(row=>{const current=values?.[row.dataset.tag];row.querySelector("[data-live-value]").textContent=current?.value??"--";row.querySelector("[data-live-updated]").textContent=current?.updated_at||"--"})}
async function loadValues(){latestValues=await api.get(`/api/v1/devices/${id}/values`)||{};applyValues(latestValues)}
```

Do not interpolate runtime values into `innerHTML`; assign them through `textContent` as shown.

- [ ] **Step 5: Wire pagination, filtering, and mutation refreshes**

Add event handlers:

```js
prevPage.onclick=()=>setPage(pageState.number-1);
nextPage.onclick=()=>setPage(pageState.number+1);
pageSize.onchange=()=>{pageState.size=Number(pageSize.value);pageState.number=1;loadPoints().catch(showError)};
```

Replace direct search loading with a 250 ms debounce and reset to page one:

```js
let searchTimer;
document.querySelector("#search").oninput=()=>{clearTimeout(searchTimer);searchTimer=setTimeout(()=>{pageState.number=1;loadPoints().catch(showError)},250)};
document.querySelector("#reg-type").onchange=()=>{pageState.number=1;loadPoints().catch(showError)};
```

Keep `refresh()` for the initial summary, points, and values load. Keep the 2-second interval limited to `loadSummary()` and `loadValues()`. After create, edit, delete, or import succeeds, call `loadPoints()` and `loadValues()` so configuration and displayed values converge immediately. `loadPoints()` itself handles falling back from an emptied last page.

- [ ] **Step 6: Implement persistent pointer-based column resizing**

Add:

```js
function readColumnWidths(){try{const widths=JSON.parse(localStorage.getItem(columnStorageKey)||"{}");return widths&&typeof widths==="object"?widths:{}}catch{return {}}}
function writeColumnWidths(widths){try{localStorage.setItem(columnStorageKey,JSON.stringify(widths))}catch{}}
function initColumnResizing(){
  const table=document.querySelector("#points-table"),widths=readColumnWidths();
  table.querySelectorAll("col[data-column]").forEach(col=>{const width=Number(widths[col.dataset.column]);if(Number.isFinite(width)&&width>=60)col.style.width=`${width}px`});
  table.querySelectorAll("[data-resize]").forEach(handle=>handle.addEventListener("pointerdown",event=>{
    event.preventDefault();
    const key=handle.dataset.resize,col=table.querySelector(`col[data-column="${key}"]`),startX=event.clientX,startWidth=col.getBoundingClientRect().width;
    document.body.classList.add("resizing-column");handle.setPointerCapture(event.pointerId);
    const move=moveEvent=>{col.style.width=`${Math.max(60,startWidth+moveEvent.clientX-startX)}px`};
    const up=()=>{handle.removeEventListener("pointermove",move);handle.removeEventListener("pointerup",up);handle.removeEventListener("pointercancel",up);document.body.classList.remove("resizing-column");const saved=readColumnWidths();saved[key]=Math.round(col.getBoundingClientRect().width);writeColumnWidths(saved)};
    handle.addEventListener("pointermove",move);handle.addEventListener("pointerup",up);handle.addEventListener("pointercancel",up);
  }));
}
```

Call `initColumnResizing()` once before the initial `refresh()`.

- [ ] **Step 7: Run focused tests and syntax-check JavaScript**

Run: `go test ./web -count=1`

Expected: PASS.

If Node.js is installed, run: `node --check web/js/device.js`

Expected: no output and exit code 0. If Node.js is unavailable, record that manual browser loading is the syntax validation fallback.

- [ ] **Step 8: Commit the browser behavior**

```powershell
git add web/js/device.js web/embed_test.go
git commit -m "feat: paginate point values in main table"
```

---

### Task 4: Verify the Integrated Feature

**Files:**
- Verify only; do not modify unrelated files.

**Interfaces:**
- Consumes: all outputs from Tasks 1-3.
- Produces: evidence that backend tests, embedded assets, formatting, vet, and the application build pass.

- [ ] **Step 1: Format all changed Go files and confirm no diff is introduced by formatting**

Run: `gofmt -w internal/httpapi/point_handler.go internal/httpapi/httpapi_test.go web/embed_test.go`

Run: `git diff --check`

Expected: no whitespace errors. Existing unrelated diffs may still be listed by `git status` and must remain untouched.

- [ ] **Step 2: Run all tests**

Run: `go test ./...`

Expected: PASS for every package.

- [ ] **Step 3: Run static analysis**

Run: `go vet ./...`

Expected: exit code 0 with no diagnostics.

- [ ] **Step 4: Build the Windows executable outside tracked output paths**

Run: `go build -o "$env:TEMP\modbus-scan-pagination-check.exe" ./cmd/modbus-scan`

Expected: exit code 0. Do not add the generated executable to Git.

- [ ] **Step 5: Perform the browser acceptance checklist**

Start the application with its existing configured command, open one device detail page, and verify:

1. Page sizes 10, 20, 50, 100, 200, and 500 change the number of rows and reset to page 1.
2. Previous/next buttons and `第 N / M 页，共 T 条` stay correct at the first, middle, and last pages.
3. Search and register-type filtering reset to page 1 and preserve the selected page size.
4. The table scrolls vertically within 60 viewport-height units and keeps its header visible.
5. Every column resizes from its right edge; a browser refresh restores the widths.
6. Each current-page row shows its own real-time value and update time; missing values show `--`.
7. Waiting through multiple 2-second refreshes does not reset the page, scroll position, or widths.
8. Changing page removes the old page's rows and only the new page rows receive value updates.
9. Add, edit, delete, and CSV import refresh the page correctly; deleting the only row on the last page moves to the preceding page.

- [ ] **Step 6: Review the final scoped diff**

Run:

```powershell
git diff HEAD~3 -- internal/httpapi/point_handler.go internal/httpapi/httpapi_test.go web/device.html web/css/app.css web/js/device.js web/embed_test.go
```

Expected: only the approved pagination, table layout, live-value merge, resizing, and tests are present.

