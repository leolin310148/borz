const $ = (id) => document.getElementById(id);

async function load() {
  const { bb = {} } = await chrome.storage.local.get("bb");
  if (bb.host) $("host").value = bb.host;
  if (bb.port) $("port").value = bb.port;
  if (bb.token) $("token").value = bb.token;
  refreshStatus();
}

async function save() {
  const bb = {
    host: $("host").value.trim() || "127.0.0.1",
    port: parseInt($("port").value, 10) || 19824,
    token: $("token").value.trim(),
  };
  await chrome.storage.local.set({ bb });
  refreshStatus();
}

async function refreshStatus() {
  const cfg = await chrome.storage.local.get("bb");
  const bb = cfg.bb || {};
  const host = bb.host || "127.0.0.1";
  const port = bb.port || 19824;
  const url = `http://${host}:${port}/v1/ext/status${bb.token ? `?token=${encodeURIComponent(bb.token)}` : ""}`;
  try {
    const resp = await fetch(url);
    if (!resp.ok) throw new Error(resp.status);
    const data = await resp.json();
    $("status").textContent = data.connected > 0 ? "Connected" : "Daemon up · ext not attached";
    $("status").className = "status " + (data.connected > 0 ? "ok" : "bad");
    $("caps").textContent = data.connected > 0
      ? "APIs: cookies, bookmarks, history, downloads, windows, tabs, browser events"
      : "Waiting for the service worker WebSocket to attach.";
  } catch {
    $("status").textContent = "Daemon unreachable";
    $("status").className = "status bad";
    $("caps").textContent = "Start borz daemon/server, then save to reconnect.";
  }
}

$("save").addEventListener("click", save);
load();
