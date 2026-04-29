// bb-browser bridge — service worker.
//
// Connects to the local daemon over WebSocket and exposes capabilities that
// the Chrome DevTools Protocol cannot reach: cross-domain chrome.cookies,
// bookmarks/history/downloads/window APIs, and browser-level events.
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
let connectedAt = 0;

const SUPPORTED_METHODS = [
  "ping",
  "capabilities",
  "cookies.getAll",
  "cookies.set",
  "cookies.remove",
  "bookmarks.getTree",
  "bookmarks.search",
  "bookmarks.create",
  "bookmarks.update",
  "bookmarks.remove",
  "history.search",
  "history.deleteUrl",
  "history.deleteRange",
  "downloads.search",
  "downloads.download",
  "downloads.erase",
  "downloads.cancel",
  "downloads.pause",
  "downloads.resume",
  "downloads.show",
  "downloads.showDefaultFolder",
  "windows.getAll",
  "windows.create",
  "windows.update",
  "windows.remove",
  "tabs.query",
  "tabs.captureVisibleTab",
  "tabs.duplicate",
  "tabs.discard",
  "tabs.reload",
  "tabGroups.query",
];

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
    connectedAt = Date.now();
    setBadge("ON", "#1a7f37");
    pushEvent("extension.connected", { ts: connectedAt, version: chrome.runtime.getManifest().version });
  };
  ws.onmessage = (ev) => handleMessage(ev.data);
  ws.onclose = () => {
    connectedAt = 0;
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
    case "ping":
      return { ok: true, ts: Date.now() };
    case "capabilities":
      return await capabilities();

    case "cookies.getAll":
      // Empty filter returns cookies for ALL domains the extension can see —
      // this is the headline capability CDP cannot provide.
      return await chrome.cookies.getAll(params.filter || {});
    case "cookies.set":
      return await chrome.cookies.set(params.details || params);
    case "cookies.remove":
      return await chrome.cookies.remove(params.details || params);

    case "bookmarks.getTree":
      return await chrome.bookmarks.getTree();
    case "bookmarks.search":
      return await chrome.bookmarks.search(params.query || params.q || "");
    case "bookmarks.create":
      return await chrome.bookmarks.create(params.bookmark || params);
    case "bookmarks.update":
      return await chrome.bookmarks.update(requireID(params), params.changes || pick(params, ["title", "url"]));
    case "bookmarks.remove":
      if (params.recursive) return await chrome.bookmarks.removeTree(requireID(params));
      return await chrome.bookmarks.remove(requireID(params));

    case "history.search":
      return await chrome.history.search(historySearchParams(params));
    case "history.deleteUrl":
      return await chrome.history.deleteUrl({ url: requireString(params.url, "url") });
    case "history.deleteRange":
      return await chrome.history.deleteRange({
        startTime: requireNumber(params.startTime, "startTime"),
        endTime: requireNumber(params.endTime, "endTime"),
      });

    case "downloads.search":
      return await chrome.downloads.search(downloadQuery(params));
    case "downloads.download":
      return await chrome.downloads.download(params.download || params);
    case "downloads.erase":
      return await chrome.downloads.erase(downloadQuery(params));
    case "downloads.cancel":
      return await chrome.downloads.cancel(requireNumber(params.id, "id"));
    case "downloads.pause":
      return await chrome.downloads.pause(requireNumber(params.id, "id"));
    case "downloads.resume":
      return await chrome.downloads.resume(requireNumber(params.id, "id"));
    case "downloads.show":
      chrome.downloads.show(requireNumber(params.id, "id"));
      return { ok: true };
    case "downloads.showDefaultFolder":
      chrome.downloads.showDefaultFolder();
      return { ok: true };

    case "windows.getAll":
      return await chrome.windows.getAll({ populate: params.populate !== false, windowTypes: params.windowTypes });
    case "windows.create":
      return await chrome.windows.create(params.createData || params);
    case "windows.update":
      return await chrome.windows.update(requireNumber(params.id, "id"), params.updateInfo || pick(params, ["focused", "drawAttention", "state", "left", "top", "width", "height"]));
    case "windows.remove":
      return await chrome.windows.remove(requireNumber(params.id, "id"));

    case "tabs.query":
      return await chrome.tabs.query(params.queryInfo || params);
    case "tabs.captureVisibleTab":
      return await chrome.tabs.captureVisibleTab(params.windowId, params.options || { format: "png" });
    case "tabs.duplicate":
      return await chrome.tabs.duplicate(requireNumber(params.id, "id"));
    case "tabs.discard":
      return await chrome.tabs.discard(params.id === undefined ? undefined : requireNumber(params.id, "id"));
    case "tabs.reload":
      return await chrome.tabs.reload(params.id === undefined ? undefined : requireNumber(params.id, "id"), params.reloadProperties || {});
    case "tabGroups.query":
      return await chrome.tabGroups.query(params.queryInfo || params);
    default:
      throw new Error(`unknown method: ${method}`);
  }
}

async function capabilities() {
  let permissions = {};
  try {
    permissions = await chrome.permissions.getAll();
  } catch (err) {
    permissions = { error: String(err && err.message || err) };
  }
  return {
    ok: true,
    name: chrome.runtime.getManifest().name,
    version: chrome.runtime.getManifest().version,
    manifestVersion: chrome.runtime.getManifest().manifest_version,
    connectedAt,
    supportedMethods: SUPPORTED_METHODS,
    permissions,
  };
}

function requireID(params) {
  return requireString(params.id, "id");
}

function requireString(value, name) {
  if (typeof value !== "string" || !value) throw new Error(`${name} is required`);
  return value;
}

function requireNumber(value, name) {
  const n = Number(value);
  if (!Number.isFinite(n)) throw new Error(`${name} must be a number`);
  return n;
}

function pick(obj, keys) {
  const out = {};
  for (const key of keys) {
    if (obj[key] !== undefined) out[key] = obj[key];
  }
  return out;
}

function historySearchParams(params) {
  const out = {
    text: params.text || params.q || "",
    maxResults: Number(params.maxResults || params.limit || 100),
  };
  if (params.startTime !== undefined) out.startTime = Number(params.startTime);
  if (params.endTime !== undefined) out.endTime = Number(params.endTime);
  return out;
}

function downloadQuery(params) {
  if (params.query && typeof params.query === "object") return params.query;
  const out = {};
  if (params.q) out.query = [String(params.q)];
  if (params.query && typeof params.query === "string") out.query = [params.query];
  for (const key of ["id", "url", "filename", "state", "mime", "danger", "exists", "paused", "limit", "orderBy", "startedAfter", "startedBefore", "endedAfter", "endedBefore", "totalBytesGreater", "totalBytesLess"]) {
    if (params[key] !== undefined) out[key] = params[key];
  }
  for (const key of ["id", "limit", "totalBytesGreater", "totalBytesLess"]) {
    if (out[key] !== undefined) out[key] = Number(out[key]);
  }
  return out;
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

chrome.windows.onCreated.addListener((window) => {
  pushEvent("windows.created", window);
});

chrome.windows.onRemoved.addListener((windowId) => {
  pushEvent("windows.removed", { id: windowId });
});

chrome.windows.onFocusChanged.addListener((windowId) => {
  pushEvent("windows.focusChanged", { id: windowId });
});

chrome.bookmarks.onCreated.addListener((id, node) => {
  pushEvent("bookmarks.created", { id, node });
});

chrome.bookmarks.onRemoved.addListener((id, removeInfo) => {
  pushEvent("bookmarks.removed", { id, ...removeInfo });
});

chrome.bookmarks.onChanged.addListener((id, changeInfo) => {
  pushEvent("bookmarks.changed", { id, ...changeInfo });
});

chrome.bookmarks.onMoved.addListener((id, moveInfo) => {
  pushEvent("bookmarks.moved", { id, ...moveInfo });
});

chrome.history.onVisited.addListener((result) => {
  pushEvent("history.visited", result);
});

chrome.history.onVisitRemoved.addListener((removed) => {
  pushEvent("history.visitRemoved", removed);
});

chrome.downloads.onCreated.addListener((item) => {
  pushEvent("downloads.created", item);
});

chrome.downloads.onChanged.addListener((delta) => {
  pushEvent("downloads.changed", delta);
});

chrome.downloads.onErased.addListener((downloadId) => {
  pushEvent("downloads.erased", { id: downloadId });
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
