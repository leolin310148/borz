// bb-browser bridge — service worker.
//
// Connects to the local daemon over WebSocket and exposes capabilities that
// the Chrome DevTools Protocol cannot reach: chrome.cookies cross-domain,
// browser-level tab events, and (later) bookmarks/history/downloads.
//
// Wire protocol (JSON):
//   daemon → extension : {type:"request", id, method, params}
//   extension → daemon : {type:"response", id, result?, error?}
//                        {type:"event", name, data}

const DEFAULT_PORT = 19824;
const DEFAULT_HOST = "127.0.0.1";
const RECONNECT_BASE_MS = 1000;
const RECONNECT_MAX_MS = 15000;

let ws = null;
let reconnectMs = RECONNECT_BASE_MS;
let connectTimer = null;

async function getConfig() {
  const { bb = {} } = await chrome.storage.local.get("bb");
  return {
    host: bb.host || DEFAULT_HOST,
    port: bb.port || DEFAULT_PORT,
    token: bb.token || "",
  };
}

function setBadge(text, color) {
  chrome.action.setBadgeText({ text });
  if (color) chrome.action.setBadgeBackgroundColor({ color });
}

async function connect() {
  if (connectTimer) {
    clearTimeout(connectTimer);
    connectTimer = null;
  }
  const cfg = await getConfig();
  const url = `ws://${cfg.host}:${cfg.port}/v1/ext/ws${
    cfg.token ? `?token=${encodeURIComponent(cfg.token)}` : ""
  }`;
  try {
    ws = new WebSocket(url);
  } catch (err) {
    scheduleReconnect();
    return;
  }
  ws.onopen = () => {
    reconnectMs = RECONNECT_BASE_MS;
    setBadge("ON", "#1a7f37");
  };
  ws.onmessage = (ev) => handleMessage(ev.data);
  ws.onclose = () => {
    setBadge("OFF", "#cf222e");
    scheduleReconnect();
  };
  ws.onerror = () => {
    // onclose will fire; reconnect there.
  };
}

function scheduleReconnect() {
  if (connectTimer) return;
  connectTimer = setTimeout(() => {
    connectTimer = null;
    connect();
  }, reconnectMs);
  reconnectMs = Math.min(reconnectMs * 2, RECONNECT_MAX_MS);
}

function send(obj) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify(obj));
  }
}

async function handleMessage(raw) {
  let msg;
  try {
    msg = JSON.parse(raw);
  } catch {
    return;
  }
  if (msg.type !== "request") return;
  try {
    const result = await dispatch(msg.method, msg.params || {});
    send({ type: "response", id: msg.id, result });
  } catch (err) {
    send({ type: "response", id: msg.id, error: String(err && err.message || err) });
  }
}

async function dispatch(method, params) {
  switch (method) {
    case "cookies.getAll":
      // Empty filter returns cookies for ALL domains the extension can see —
      // this is the headline capability CDP cannot provide.
      return await chrome.cookies.getAll({});
    case "ping":
      return { ok: true, ts: Date.now() };
    default:
      throw new Error(`unknown method: ${method}`);
  }
}

// --- Tab event push ---

function pushEvent(name, data) {
  send({ type: "event", name, data });
}

chrome.tabs.onCreated.addListener((tab) => {
  pushEvent("tabs.created", {
    id: tab.id,
    windowId: tab.windowId,
    url: tab.url || tab.pendingUrl || "",
    index: tab.index,
  });
});

chrome.tabs.onRemoved.addListener((tabId, info) => {
  pushEvent("tabs.removed", { id: tabId, windowId: info.windowId, isWindowClosing: info.isWindowClosing });
});

chrome.tabs.onUpdated.addListener((tabId, change, tab) => {
  if (!change.url && !change.status && !change.title) return;
  pushEvent("tabs.updated", {
    id: tabId,
    url: tab.url,
    title: tab.title,
    status: tab.status,
    change,
  });
});

chrome.tabs.onActivated.addListener((info) => {
  pushEvent("tabs.activated", { id: info.tabId, windowId: info.windowId });
});

// --- Service worker keepalive ---
//
// MV3 service workers idle out after ~30s. A periodic alarm keeps it alive
// long enough for the WebSocket to stay attached and events to flow. The
// daemon also pings every 30s, which serves as a secondary keepalive.

chrome.alarms.create("bb-keepalive", { periodInMinutes: 0.5 });
chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name !== "bb-keepalive") return;
  if (!ws || ws.readyState === WebSocket.CLOSED) {
    connect();
  }
});

chrome.runtime.onStartup.addListener(connect);
chrome.runtime.onInstalled.addListener(connect);

// First boot of the worker after install/update.
connect();

// React to config changes from the popup without reload.
chrome.storage.onChanged.addListener((changes, area) => {
  if (area !== "local" || !changes.bb) return;
  if (ws) {
    try { ws.close(); } catch {}
  }
  reconnectMs = RECONNECT_BASE_MS;
  connect();
});
