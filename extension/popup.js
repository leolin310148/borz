const $ = (id) => document.getElementById(id);

async function load() {
  const { bb = {} } = await chrome.storage.local.get("bb");
  if (bb.host) $("host").value = bb.host;
  if (bb.port) $("port").value = bb.port;
  if (bb.profile) $("profile").value = bb.profile;
  if (bb.token) $("token").value = bb.token;
  refreshStatus();
}

async function save() {
  const bb = {
    host: $("host").value.trim() || "127.0.0.1",
    port: parseInt($("port").value, 10) || 19824,
    profile: $("profile").value.trim() || "default",
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
  const profile = (bb.profile || "default").trim() || "default";
  const query = new URLSearchParams({ profile });
  if (bb.token) query.set("token", bb.token);
  const url = `http://${host}:${port}/v1/ext/status?${query}`;
  $("token-hint").innerHTML = `Run <code>borz --profile ${escapeHTML(profile)} daemon token --copy</code>, then paste.`;
  try {
    const resp = await fetch(url);
    if (resp.status === 401) {
      $("status").textContent = "Authentication failed";
      $("status").className = "status bad";
      $("caps").textContent = `Token rejected. Run: borz --profile ${profile} daemon token --copy`;
      return;
    }
    if (resp.status === 409) {
      const mismatch = await resp.json().catch(() => ({}));
      $("status").textContent = "Profile mismatch";
      $("status").className = "status bad";
      $("caps").textContent = `This daemon is profile ${mismatch.expectedProfile || "?"}, but the popup expects ${profile}.`;
      return;
    }
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
    const data = await resp.json();
    const daemonProfile = data.profile || profile;
    $("status").textContent = data.connected > 0 ? `Connected · ${daemonProfile}` : `Daemon up · ${daemonProfile} · ext not attached`;
    $("status").className = "status " + (data.connected > 0 ? "ok" : "bad");
    $("caps").textContent = data.connected > 0
      ? "APIs: cookies, bookmarks, history, downloads, windows, tabs, browser events"
      : "Waiting for the service worker WebSocket to attach.";
  } catch (err) {
    $("status").textContent = "Daemon unreachable";
    $("status").className = "status bad";
    $("caps").textContent = `Cannot reach http://${host}:${port}. Start borz daemon/server, then save to reconnect. (${err.message || err})`;
  }
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, (ch) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[ch]));
}

async function recording(action) {
  const cfg = await chrome.storage.local.get("bb");
  const bb = cfg.bb || {};
  const host = bb.host || "127.0.0.1";
  const port = bb.port || 19824;
  const profile = (bb.profile || "default").trim() || "default";
  const query = new URLSearchParams({ profile });
  if (bb.token) query.set("token", bb.token);
  const suffix = `?${query}`;
  if (action === "start") {
    const body = { mode: "client" };
    const resp = await fetch(`http://${host}:${port}/v1/recordings${suffix}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!resp.ok) throw new Error(await resp.text());
  } else {
    const resp = await fetch(`http://${host}:${port}/v1/recordings/current/stop${suffix}`, { method: "POST" });
    if (!resp.ok) throw new Error(await resp.text());
  }
  refreshStatus();
}

$("save").addEventListener("click", save);
$("record-start").addEventListener("click", () => recording("start").catch((err) => {
  $("status").textContent = err.message || String(err);
  $("status").className = "status bad";
}));
$("record-stop").addEventListener("click", () => recording("stop").catch((err) => {
  $("status").textContent = err.message || String(err);
  $("status").className = "status bad";
}));
load();
