"use strict";

const id = new URLSearchParams(location.search).get("id");
if (!/^\d+$/.test(id || "")) location.href = "/";

const pointsBody = document.querySelector("#points");
const dialog = document.querySelector("#point-dialog");
const form = document.querySelector("#point-form");
const pageSize = document.querySelector("#page-size");
const pageStatus = document.querySelector("#page-status");
const prevPage = document.querySelector("#prev-page");
const nextPage = document.querySelector("#next-page");
const deletePage = document.querySelector("#delete-page");
const realtimeDialog = document.querySelector("#realtime-dialog");
const realtimeWindow = document.querySelector("#realtime-window");
const pageState = { number: 1, size: 20, total: 0 };
const CHART_WINDOW_MINUTES = Object.freeze([1, 5, 10, 30]);
const DEFAULT_CHART_WINDOW_MINUTES = 5;
const MAX_CHART_AGE_MS = 30 * 60 * 1000;
const columnStorageKey = "modbus-scan.point-column-widths.v1";
let busy = false;
let pointsRequest = 0;
let pageItems = [];
let realtimeSocket = null;
let realtimeReconnectTimer = null;
let realtimeRedrawTimer = null;
let realtimeSession = 0;
let realtimeSamples = [];
let realtimeWindowMinutes = DEFAULT_CHART_WINDOW_MINUTES;
let realtimeConnected = false;
let realtimeSegment = 0;
let realtimeLatestValue = null;
let realtimeHoverX = null;
const deviceKafkaForm = document.querySelector("#device-kafka-form");

const esc = value => String(value ?? "").replace(/[&<>"']/g, char => ({
  "&": "&amp;",
  "<": "&lt;",
  ">": "&gt;",
  '"': "&quot;",
  "'": "&#39;",
}[char]));

const totalPages = () => Math.max(1, Math.ceil(pageState.total / pageState.size));

function updatePager() {
  pageStatus.textContent = `第 ${pageState.number} / ${totalPages()} 页，共 ${pageState.total} 条`;
  prevPage.disabled = pageState.number <= 1;
  nextPage.disabled = pageState.number >= totalPages();
}

function setPage(number) {
  const next = Math.min(Math.max(1, number), totalPages());
  if (next === pageState.number) return;
  pageState.number = next;
  loadPoints().catch(showError);
}

function setLifecycleBusy(busy) {
  document.querySelectorAll("#controls [data-action]").forEach(button => {
    button.disabled = busy;
  });
}

function renderRuntime(runtime) {
  const state = document.querySelector("#runtime-state");
  if (!state) return;
  state.className = `badge ${runtime.state}`;
  state.textContent = runtime.state;
  document.querySelector("#config-pending").hidden = !runtime.config_pending;
  document.querySelector("#runtime-error").textContent = runtime.last_error || "";
  setLifecycleBusy(Boolean(runtime.operation));
}

function renderSummary(device, runtime) {
  document.querySelector("#title").textContent = device.name;
  document.querySelector("#summary").innerHTML = `<b>${esc(device.address)}:${device.port}</b> · Slave ${device.slave_id} · <span id="runtime-state" class="badge"></span> <span id="config-pending" class="pending" hidden>配置待重启生效</span><p id="runtime-error"></p>`;
  document.querySelector("#export").href = `/api/v1/devices/${id}/points/export`;
  renderRuntime(runtime);
}

async function loadSummary() {
  const view = await api.get(`/api/v1/devices/${id}`);
  renderSummary(view.device, view.runtime);
}

async function loadStatus() {
  const runtime = await api.get(`/api/v1/devices/${id}/status`);
  renderRuntime(runtime);
}

function renderPoints(points) {
  pointsBody.innerHTML = points.map(point => `<tr><td>${esc(point.tag_name)}</td><td>${esc(point.reg_type)}</td><td>${point.address}</td><td>${esc(point.data_type)}</td><td>${point.bit_offset}</td><td>${point.bit_len}</td><td>${point.writeable === 1 ? "读写" : "只读"}</td><td>${esc(point.description)}</td><td class="sticky-actions"><button data-realtime='${JSON.stringify(point).replace(/'/g, "&#39;")}'>实时值</button> <button data-edit='${JSON.stringify(point).replace(/'/g, "&#39;")}'>编辑</button> <button class="danger" data-delete="${point.id}">删除</button></td></tr>`).join("");
}

async function loadPoints() {
  const requestID = ++pointsRequest;
  const offset = (pageState.number - 1) * pageState.size;
  const search = encodeURIComponent(document.querySelector("#search").value);
  const type = encodeURIComponent(document.querySelector("#reg-type").value);
  prevPage.disabled = true;
  nextPage.disabled = true;
  try {
    const page = await api.get(`/api/v1/devices/${id}/points?limit=${pageState.size}&offset=${offset}&search=${search}&reg_type=${type}`);
    if (requestID !== pointsRequest) return;
    pageState.total = page.total;
    if (pageState.number > totalPages()) {
      pageState.number = totalPages();
      await loadPoints();
      return;
    }
    pageItems = page.items || [];
    deletePage.disabled = pageItems.length === 0;
    renderPoints(pageItems);
  } finally {
    if (requestID === pointsRequest) updatePager();
  }
}

async function refresh() {
  if (busy) return;
  busy = true;
  try {
    await Promise.all([loadSummary(), loadPoints()]);
  } catch (error) {
    showError(error);
  } finally {
    busy = false;
  }
}

document.querySelector("#controls").onclick = async event => {
  const button = event.target.closest("[data-action]");
  if (!button) return;
  try {
    setLifecycleBusy(true);
    await api.post(`/api/v1/devices/${id}/${button.dataset.action}`);
    await refresh();
  } catch (error) {
    showError(error);
    if (error.code === "device_busy") await loadStatus();
    else setLifecycleBusy(false);
  }
};

document.querySelector("#add-point").onclick = () => {
  form.reset();
  document.querySelector("#point-id").value = "";
  dialog.showModal();
};

document.querySelectorAll("[data-close]").forEach(element => {
  element.onclick = () => dialog.close();
});

function formatChartValue(value) {
  if (Number.isInteger(value)) return String(value);
  return Math.abs(value) < 10 ? value.toFixed(2).replace(/\.?0+$/, "") : value.toFixed(1).replace(/\.0$/, "");
}

const INTEGER_DATA_TYPES = new Set(["Int16", "UInt16", "Int32", "UInt32"]);

function formatRadix(integer, radix, prefix) {
  const sign = integer < 0 ? "-" : "";
  return `${sign}${prefix}${Math.abs(integer).toString(radix).toUpperCase()}`;
}

function formatRealtimeFormats(value, dataType) {
  if (value === null || value === undefined || value === "") return {decimal: "—", binary: "—", hexadecimal: "—"};
  const numeric = typeof value === "boolean" ? (value ? 1 : 0) : Number(value);
  if (!Number.isFinite(numeric)) return {decimal: "—", binary: "—", hexadecimal: "—"};
  const decimal = typeof value === "boolean" ? String(numeric) : String(value);
  if (dataType !== "Bool" && !INTEGER_DATA_TYPES.has(dataType)) return {decimal, binary: "—", hexadecimal: "—"};
  if (!Number.isInteger(numeric)) return {decimal, binary: "—", hexadecimal: "—"};
  const integer = numeric;
  return {
    decimal,
    binary: formatRadix(integer, 2, "0b"),
    hexadecimal: formatRadix(integer, 16, "0x"),
  };
}

function renderRealtimeFormats(value, dataType) {
  const formats = formatRealtimeFormats(value, dataType);
  document.querySelector("#realtime-decimal").textContent = formats.decimal;
  document.querySelector("#realtime-binary").textContent = formats.binary;
  document.querySelector("#realtime-hexadecimal").textContent = formats.hexadecimal;
}

function prepareRealtimeCanvas(canvas) {
  const bounds = canvas.getBoundingClientRect();
  const width = Math.max(320, Math.round(bounds.width || 800));
  const height = Math.max(220, Math.round(bounds.height || 320));
  const ratio = window.devicePixelRatio || 1;
  canvas.width = Math.round(width * ratio);
  canvas.height = Math.round(height * ratio);
  const ctx = canvas.getContext("2d");
  ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
  return {ctx, width, height};
}

function formatChartTime(at) {
  const date = new Date(at);
  return [date.getHours(), date.getMinutes(), date.getSeconds()].map(part => String(part).padStart(2, "0")).join(":");
}

function drawChartAxes(ctx, plot, min, max, windowStart, now) {
  ctx.font = "12px system-ui";
  ctx.lineWidth = 1;
  ctx.strokeStyle = "#e5e7eb";
  ctx.fillStyle = "#4b5563";
  ctx.textBaseline = "middle";
  for (let step = 0; step <= 4; step++) {
    const y = plot.bottom - (plot.bottom - plot.top) * step / 4;
    ctx.beginPath(); ctx.moveTo(plot.left, y); ctx.lineTo(plot.right, y); ctx.stroke();
    ctx.textAlign = "right";
    ctx.fillText(formatChartValue(min + (max - min) * step / 4), plot.left - 8, y);
  }
  ctx.textBaseline = "top";
  for (let step = 0; step <= 5; step++) {
    const x = plot.left + (plot.right - plot.left) * step / 5;
    ctx.beginPath(); ctx.moveTo(x, plot.top); ctx.lineTo(x, plot.bottom); ctx.stroke();
    ctx.textAlign = step === 0 ? "left" : step === 5 ? "right" : "center";
    ctx.fillText(formatChartTime(windowStart + (now - windowStart) * step / 5), x, plot.bottom + 8);
  }
  ctx.strokeStyle = "#6b7280";
  ctx.beginPath();
  ctx.moveTo(plot.left, plot.top); ctx.lineTo(plot.left, plot.bottom); ctx.lineTo(plot.right, plot.bottom);
  ctx.stroke();
}

function sampleRealtimeValue() {
  const now = Date.now();
  const cutoff = now - MAX_CHART_AGE_MS;
  realtimeSamples = realtimeSamples.filter(sample => sample.at >= cutoff);
  if (realtimeConnected && realtimeSocket?.readyState === WebSocket.OPEN && realtimeLatestValue !== null) {
    const at = Math.floor(Date.now() / 1000) * 1000;
    const latest = realtimeSamples.at(-1);
    const sample = {value: realtimeLatestValue, at, segment: realtimeSegment};
    if (latest?.at === at && latest.segment === realtimeSegment) realtimeSamples[realtimeSamples.length - 1] = sample;
    else realtimeSamples.push(sample);
  }
  drawRealtimeChart();
}

function drawChartTooltip(ctx, plot, sample, x, y) {
  ctx.save();
  ctx.strokeStyle = "#64748b";
  ctx.lineWidth = 1;
  ctx.setLineDash([4, 4]);
  ctx.beginPath(); ctx.moveTo(x, plot.top); ctx.lineTo(x, plot.bottom); ctx.stroke();
  ctx.setLineDash([]);
  ctx.fillStyle = "#2563eb";
  ctx.beginPath(); ctx.arc(x, y, 4, 0, Math.PI * 2); ctx.fill();
  const text = `${formatChartTime(sample.at)}  ${formatChartValue(sample.value)}`;
  ctx.font = "12px system-ui";
  const boxWidth = ctx.measureText(text).width + 16;
  const boxHeight = 28;
  const boxX = x + boxWidth + 12 <= plot.right ? x + 8 : x - boxWidth - 8;
  const boxY = Math.max(plot.top + 4, Math.min(y - boxHeight - 8, plot.bottom - boxHeight - 4));
  ctx.fillStyle = "#111827";
  ctx.fillRect(boxX, boxY, boxWidth, boxHeight);
  ctx.fillStyle = "#fff";
  ctx.textAlign = "left";
  ctx.textBaseline = "middle";
  ctx.fillText(text, boxX + 8, boxY + boxHeight / 2);
  ctx.restore();
}

function drawRealtimeChart() {
  const canvas = document.querySelector("#realtime-chart");
  const {ctx, width, height} = prepareRealtimeCanvas(canvas);
  ctx.clearRect(0, 0, width, height);
  if (!CHART_WINDOW_MINUTES.includes(realtimeWindowMinutes)) realtimeWindowMinutes = DEFAULT_CHART_WINDOW_MINUTES;
  const now = Date.now();
  const windowMilliseconds = realtimeWindowMinutes * 60 * 1000;
  const windowStart = now - windowMilliseconds;
  const visible = realtimeSamples.filter(sample => sample.at >= windowStart);
  const anchor = realtimeSamples.filter(sample => sample.at < windowStart).at(-1);
  if (anchor) visible.unshift({...anchor, at: windowStart});
  let min = 0, max = 1;
  if (visible.length) {
    const values = visible.map(sample => sample.value);
    min = Math.min(...values); max = Math.max(...values);
    if (min === max) { const padding = Math.max(Math.abs(min) * 0.05, 1); min -= padding; max += padding; }
  }
  const plot = {left: 64, top: 16, right: width - 16, bottom: height - 40};
  drawChartAxes(ctx, plot, min, max, windowStart, now);
  if (!visible.length) return;
  const xFor = at => plot.left + (Math.min(Math.max(at, windowStart), now) - windowStart) * (plot.right - plot.left) / windowMilliseconds;
  const yFor = value => plot.bottom - (value - min) * (plot.bottom - plot.top) / (max - min);
  ctx.strokeStyle = "#2563eb"; ctx.fillStyle = "#2563eb"; ctx.lineWidth = 2; ctx.beginPath();
  visible.forEach((sample, index) => {
    const x = xFor(sample.at), y = yFor(sample.value);
    index && sample.segment === visible[index - 1].segment ? ctx.lineTo(x, y) : ctx.moveTo(x, y);
  });
  const latest = visible[visible.length - 1];
  if (realtimeConnected && realtimeSocket?.readyState === WebSocket.OPEN && realtimeLatestValue !== null && latest.segment === realtimeSegment) ctx.lineTo(xFor(now), yFor(latest.value));
  ctx.stroke();
  if (visible.length === 1) { ctx.beginPath(); ctx.arc(xFor(visible[0].at), yFor(visible[0].value), 3, 0, Math.PI * 2); ctx.fill(); }
  const hoverSamples = realtimeSamples.filter(sample => sample.at >= windowStart && sample.at <= now);
  if (realtimeHoverX !== null && realtimeHoverX >= plot.left && realtimeHoverX <= plot.right && hoverSamples.length) {
    const hovered = hoverSamples.reduce((nearest, sample) => Math.abs(xFor(sample.at) - realtimeHoverX) < Math.abs(xFor(nearest.at) - realtimeHoverX) ? sample : nearest);
    drawChartTooltip(ctx, plot, hovered, xFor(hovered.at), yFor(hovered.value));
  }
}

function closeRealtime() {
  realtimeSession++;
  realtimeConnected = false;
  if (realtimeSocket) realtimeSocket.close();
  clearTimeout(realtimeReconnectTimer);
  clearInterval(realtimeRedrawTimer);
  realtimeReconnectTimer = null;
  realtimeRedrawTimer = null;
  realtimeSocket = null; realtimeSamples = [];
  realtimeLatestValue = null;
  realtimeHoverX = null;
  if (realtimeDialog.open) realtimeDialog.close();
}

function openRealtime(point) {
  closeRealtime();
  const session = ++realtimeSession;
  document.querySelector("#realtime-tag").textContent = point.tag_name;
  document.querySelector("#realtime-description").textContent = point.description || "";
  renderRealtimeFormats(null, point.data_type);
  document.querySelector("#realtime-updated").textContent = "--";
  document.querySelector("#realtime-status").textContent = "连接中";
  realtimeWindowMinutes = DEFAULT_CHART_WINDOW_MINUTES;
  realtimeSegment = 0;
  realtimeLatestValue = null;
  realtimeHoverX = null;
  realtimeWindow.value = String(DEFAULT_CHART_WINDOW_MINUTES);
  realtimeDialog.showModal();
  drawRealtimeChart();
  realtimeRedrawTimer = setInterval(sampleRealtimeValue, 1000);
  let attempts = 0;
  const connect = () => {
    if (session !== realtimeSession) return;
    const scheme = location.protocol === "https:" ? "wss" : "ws";
    const socket = new WebSocket(`${scheme}://${location.host}/api/v1/ws`);
    realtimeSocket = socket;
    socket.onopen = () => { if (session === realtimeSession) { attempts = 0; realtimeConnected = true; document.querySelector("#realtime-status").textContent = "已连接"; socket.send(JSON.stringify({action:"subscribe", device_id:+id, tags:[point.tag_name]})); drawRealtimeChart(); } };
    socket.onmessage = event => {
    if (session !== realtimeSession) return;
    const message = JSON.parse(event.data);
    if (message.type !== "value" || message.device_id !== +id || message.tag_name !== point.tag_name) return;
    renderRealtimeFormats(message.value, point.data_type);
    document.querySelector("#realtime-updated").textContent = message.updated_at || "--";
    const numeric = typeof message.value === "boolean" ? (message.value ? 1 : 0) : Number(message.value);
    realtimeLatestValue = Number.isFinite(numeric) ? numeric : null;
    };
    socket.onclose = () => { if (session === realtimeSession) { realtimeConnected = false; realtimeLatestValue = null; realtimeSegment++; drawRealtimeChart(); document.querySelector("#realtime-status").textContent = "重连中"; const delay = [500, 1000, 2000, 5000][Math.min(attempts++, 3)]; realtimeReconnectTimer = setTimeout(connect, delay); } };
  };
  connect();
}

document.querySelector("#close-realtime").onclick = closeRealtime;
realtimeDialog.addEventListener("close", () => { if (realtimeSocket) closeRealtime(); });
realtimeWindow.addEventListener("change", () => {
  const minutes = Number(realtimeWindow.value);
  realtimeWindowMinutes = CHART_WINDOW_MINUTES.includes(minutes) ? minutes : DEFAULT_CHART_WINDOW_MINUTES;
  realtimeWindow.value = String(realtimeWindowMinutes);
  drawRealtimeChart();
});
window.addEventListener("resize", () => {
  if (realtimeDialog.open) drawRealtimeChart();
});
deletePage.onclick = async () => {
  const ids = pageItems.map(point => point.id);
  if (!ids.length || !confirm(`确认删除当前页的 ${ids.length} 个点位？`)) return;
  deletePage.disabled = true;
  try { await api.delete(`/api/v1/devices/${id}/points`, {ids}); await loadPoints(); } catch (error) { showError(error); }
  finally { deletePage.disabled = pageItems.length === 0; }
};

pointsBody.onclick = async event => {
  const realtime = event.target.closest("[data-realtime]");
  if (realtime) { openRealtime(JSON.parse(realtime.dataset.realtime)); return; }
  const edit = event.target.closest("[data-edit]");
  if (edit) {
    const point = JSON.parse(edit.dataset.edit);
    for (const [field, key] of [["point-id", "id"], ["tag-name", "tag_name"], ["description", "description"], ["point-reg-type", "reg_type"], ["point-address", "address"], ["data-type", "data_type"], ["bit-offset", "bit_offset"], ["bit-len", "bit_len"]]) {
      document.querySelector(`#${field}`).value = point[key];
    }
    document.querySelector("#writeable").checked = point.writeable === 1;
    dialog.showModal();
    return;
  }
  const remove = event.target.closest("[data-delete]");
  if (remove && confirm("确认删除点位？")) {
    try {
      await api.delete(`/api/v1/devices/${id}/points/${remove.dataset.delete}`);
      await loadPoints();
    } catch (error) {
      showError(error);
    }
  }
};

form.onsubmit = async event => {
  event.preventDefault();
  const pointID = document.querySelector("#point-id").value;
  const point = {
    tag_name: document.querySelector("#tag-name").value,
    description: document.querySelector("#description").value,
    reg_type: document.querySelector("#point-reg-type").value,
    address: +document.querySelector("#point-address").value,
    data_type: document.querySelector("#data-type").value,
    bit_offset: +document.querySelector("#bit-offset").value,
    bit_len: +document.querySelector("#bit-len").value,
    writeable: document.querySelector("#writeable").checked ? 1 : 0,
  };
  try {
    if (pointID) await api.put(`/api/v1/devices/${id}/points/${pointID}`, point);
    else await api.post(`/api/v1/devices/${id}/points`, point);
    dialog.close();
    await loadPoints();
  } catch (error) {
    showError(error);
  }
};

document.querySelector("#csv-file").onchange = async event => {
  const file = event.target.files[0];
  if (!file || !confirm(`用 ${file.name} 全量替换当前点位？`)) return;
  try {
    await api.upload(`/api/v1/devices/${id}/points/import`, file);
    pageState.number = 1;
    await loadPoints();
  } catch (error) {
    showError(error);
  } finally {
    event.target.value = "";
  }
};

prevPage.onclick = () => setPage(pageState.number - 1);
nextPage.onclick = () => setPage(pageState.number + 1);
pageSize.onchange = () => {
  pageState.size = Number(pageSize.value);
  pageState.number = 1;
  loadPoints().catch(showError);
};

let searchTimer;
document.querySelector("#search").oninput = () => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => {
    pageState.number = 1;
    loadPoints().catch(showError);
  }, 250);
};
document.querySelector("#reg-type").onchange = () => {
  pageState.number = 1;
  loadPoints().catch(showError);
};

function readColumnWidths() {
  try {
    const widths = JSON.parse(localStorage.getItem(columnStorageKey) || "{}");
    return widths && typeof widths === "object" ? widths : {};
  } catch {
    return {};
  }
}

function writeColumnWidths(widths) {
  try {
    localStorage.setItem(columnStorageKey, JSON.stringify(widths));
  } catch {
    // Storage can be unavailable in private or restricted browser contexts.
  }
}

function initColumnResizing() {
  const table = document.querySelector("#points-table");
  const widths = readColumnWidths();
  table.querySelectorAll("col[data-column]").forEach(column => {
    const width = Number(widths[column.dataset.column]);
    if (Number.isFinite(width) && width >= 60) column.style.width = `${width}px`;
  });
  table.querySelectorAll("[data-resize]").forEach(handle => handle.addEventListener("pointerdown", event => {
    event.preventDefault();
    const key = handle.dataset.resize;
    const column = table.querySelector(`col[data-column="${key}"]`);
    const startX = event.clientX;
    const startWidth = column.getBoundingClientRect().width;
    document.body.classList.add("resizing-column");
    handle.setPointerCapture(event.pointerId);
    const move = moveEvent => {
      column.style.width = `${Math.max(60, startWidth + moveEvent.clientX - startX)}px`;
    };
    const up = () => {
      handle.removeEventListener("pointermove", move);
      handle.removeEventListener("pointerup", up);
      handle.removeEventListener("pointercancel", up);
      document.body.classList.remove("resizing-column");
      const saved = readColumnWidths();
      saved[key] = Math.round(column.getBoundingClientRect().width);
      writeColumnWidths(saved);
    };
    handle.addEventListener("pointermove", move);
    handle.addEventListener("pointerup", up);
    handle.addEventListener("pointercancel", up);
  }));
}

const realtimeCanvas = document.querySelector("#realtime-chart");
realtimeCanvas.addEventListener("pointermove", event => {
  realtimeHoverX = event.clientX - realtimeCanvas.getBoundingClientRect().left;
  drawRealtimeChart();
});
realtimeCanvas.addEventListener("pointerleave", () => {
  realtimeHoverX = null;
  drawRealtimeChart();
});

initColumnResizing();
async function loadDeviceKafka(){const cfg=await api.get(`/api/v1/devices/${id}/kafka`);document.querySelector("#device-kafka-enabled").checked=cfg.enabled;document.querySelector("#device-kafka-topic").value=cfg.topic;document.querySelector(`input[name="kafka-mode"][value="${cfg.mode}"]`).checked=true;document.querySelector("#device-kafka-interval").value=cfg.full_interval_sec;document.querySelector("#device-kafka-interval").disabled=cfg.mode==="change"}
document.querySelectorAll('input[name="kafka-mode"]').forEach(input=>input.onchange=()=>{document.querySelector("#device-kafka-interval").disabled=input.value==="change"&&input.checked});
deviceKafkaForm.onsubmit=async event=>{event.preventDefault();const mode=document.querySelector('input[name="kafka-mode"]:checked').value;try{await api.put(`/api/v1/devices/${id}/kafka`,{enabled:document.querySelector("#device-kafka-enabled").checked,topic:document.querySelector("#device-kafka-topic").value,mode,full_interval_sec:+document.querySelector("#device-kafka-interval").value});await loadDeviceKafka()}catch(error){showError(error)}};
loadDeviceKafka().catch(showError);
refresh();
setInterval(() => {
  if (!document.hidden) {
    loadStatus().catch(showError);
  }
}, 2000);
