"use strict";

const tbody = document.querySelector("#devices");
const empty = document.querySelector("#empty");
const dialog = document.querySelector("#device-dialog");
const form = document.querySelector("#device-form");
let busy = false;
const kafkaDialog = document.querySelector("#kafka-dialog");
const kafkaForm = document.querySelector("#kafka-form");

const esc = value => String(value ?? "").replace(/[&<>"']/g, char => ({
  "&": "&amp;",
  "<": "&lt;",
  ">": "&gt;",
  '"': "&quot;",
  "'": "&#39;",
}[char]));

async function load() {
  if (busy) return;
  busy = true;
  try {
    const rows = await api.get("/api/v1/devices");
    empty.hidden = rows.length > 0;
    tbody.innerHTML = rows.map(({device: d, runtime: r, point_count: count}) => {
      const operating = Boolean(r.operation);
      const disabled = operating ? "disabled" : "";
      return `<tr><td><a href="/device.html?id=${d.id}">${esc(d.name)}</a>${r.config_pending ? '<div class="pending">配置待生效</div>' : ""}</td><td>${esc(d.address)}:${d.port}</td><td><span class="badge ${r.state}">${esc(r.state)}</span></td><td>${count}</td><td>${esc(r.last_collected_at || "--")}</td><td class="actions"><button data-edit='${JSON.stringify(d).replace(/'/g, "&#39;")}'>编辑</button><button data-lifecycle data-op="${d.enabled ? "stop" : "start"}" data-id="${d.id}" ${disabled}>${d.enabled ? "停止" : "启动"}</button><button data-lifecycle data-op="restart" data-id="${d.id}" ${disabled}>重启</button><button class="danger" data-op="delete" data-id="${d.id}">删除</button></td></tr>`;
    }).join("");
  } catch (error) {
    showError(error);
  } finally {
    busy = false;
  }
}

document.querySelector("#add-device").onclick = () => {
  form.reset();
  document.querySelector("#device-id").value = "";
  dialog.showModal();
};
document.querySelectorAll("[data-close]").forEach(element => {
  element.onclick = () => dialog.close();
});

tbody.onclick = async event => {
  const edit = event.target.closest("[data-edit]");
  if (edit) {
    const device = JSON.parse(edit.dataset.edit);
    for (const [field, key] of [["device-id", "id"], ["name", "name"], ["address", "address"], ["port", "port"], ["slave-id", "slave_id"], ["byte-order", "byte_order"], ["timeout", "timeout_sec"], ["interval", "scan_interval_ms"]]) {
      document.querySelector(`#${field}`).value = device[key];
    }
    dialog.showModal();
    return;
  }
  const button = event.target.closest("[data-op]");
  if (!button) return;
  try {
    if (button.dataset.op === "delete") {
      button.disabled = true;
      if (confirm("确认删除设备及其全部点位？")) {
        await api.delete(`/api/v1/devices/${button.dataset.id}`);
      }
    } else {
      tbody.querySelectorAll(`[data-lifecycle][data-id="${button.dataset.id}"]`).forEach(element => {
        element.disabled = true;
      });
      await api.post(`/api/v1/devices/${button.dataset.id}/${button.dataset.op}`);
    }
    await load();
  } catch (error) {
    showError(error);
    if (error.code === "device_busy") await load();
    else if (button.dataset.op === "delete") button.disabled = false;
    else tbody.querySelectorAll(`[data-lifecycle][data-id="${button.dataset.id}"]`).forEach(element => {
      element.disabled = false;
    });
  }
};

form.onsubmit = async event => {
  event.preventDefault();
  const id = document.querySelector("#device-id").value;
  const data = {
    name: document.querySelector("#name").value,
    address: document.querySelector("#address").value,
    port: +document.querySelector("#port").value,
    slave_id: +document.querySelector("#slave-id").value,
    byte_order: document.querySelector("#byte-order").value,
    timeout_sec: +document.querySelector("#timeout").value,
    scan_interval_ms: +document.querySelector("#interval").value,
  };
  try {
    if (id) await api.put(`/api/v1/devices/${id}`, data);
    else await api.post("/api/v1/devices", data);
    dialog.close();
    await load();
  } catch (error) {
    showError(error);
  }
};

document.addEventListener("visibilitychange", () => {});
load();
setInterval(() => { if (!document.hidden) load(); }, 2000);
setInterval(() => { if (document.hidden) load(); }, 10000);

async function openKafkaSettings(){const view=await api.get("/api/v1/kafka"),cfg=view.settings,q=x=>document.querySelector(x);q("#kafka-state").textContent=view.status.state;q("#kafka-error").textContent=view.status.last_error||"";q("#kafka-enabled").checked=cfg.enabled;q("#kafka-brokers").value=(cfg.brokers||[]).join("\n");for(const[f,k]of[["kafka-client-id","client_id"],["kafka-version","kafka_version"],["kafka-security","security_protocol"],["kafka-mechanism","sasl_mechanism"],["kafka-username","sasl_username"],["kafka-ca","ssl_ca_location"],["kafka-cert","ssl_certificate_location"],["kafka-key","ssl_key_location"],["kafka-endpoint","ssl_endpoint_identification_algorithm"],["kafka-queue","queue_capacity"]])q(`#${f}`).value=cfg[k]??"";q("#kafka-password").value="";q("#kafka-clear-password").checked=false;kafkaDialog.showModal()}
document.querySelector("#kafka-settings").onclick=()=>openKafkaSettings().catch(showError);document.querySelector("[data-kafka-close]").onclick=()=>kafkaDialog.close();
kafkaForm.onsubmit=async event=>{event.preventDefault();const q=x=>document.querySelector(x),data={enabled:q("#kafka-enabled").checked,brokers:q("#kafka-brokers").value.split(/\r?\n/).map(x=>x.trim()).filter(Boolean),client_id:q("#kafka-client-id").value,kafka_version:q("#kafka-version").value,security_protocol:q("#kafka-security").value,sasl_mechanism:q("#kafka-mechanism").value,sasl_username:q("#kafka-username").value,sasl_password:q("#kafka-password").value,clear_sasl_password:q("#kafka-clear-password").checked,ssl_ca_location:q("#kafka-ca").value,ssl_certificate_location:q("#kafka-cert").value,ssl_key_location:q("#kafka-key").value,ssl_endpoint_identification_algorithm:q("#kafka-endpoint").value,queue_capacity:+q("#kafka-queue").value};try{await api.put("/api/v1/kafka",data);kafkaDialog.close()}catch(error){showError(error)}};
