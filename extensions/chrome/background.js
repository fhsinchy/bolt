const HOST_NAME = "com.fhsinchy.bolt";

// --- Connection state ---
// "host_unavailable" | "daemon_unavailable" | "ready"
let connectionState = "host_unavailable";
let port = null;
let pendingCallbacks = new Map(); // id -> resolve
let nextId = 1;
let failureNotified = false;
let reinitiatedUrls = new Set();

function generateTraceID() {
  const bytes = new Uint8Array(3);
  crypto.getRandomValues(bytes);
  return "ext-" + Array.from(bytes).map(b => b.toString(16).padStart(2, "0")).join("");
}

function debugLog(...args) {
  if (cachedSettings.debugLogging) {
    console.log("[bolt]", ...args);
  }
}

// --- Port management ---

// Serializes port initialization. Only one ensurePort() runs at a time;
// concurrent callers wait for the in-flight attempt to resolve.
let portPromise = null;

function ensurePort() {
  if (portPromise) return portPromise;
  portPromise = _initPort().finally(() => { portPromise = null; });
  return portPromise;
}

function _initPort() {
  return new Promise((resolve) => {
    if (port) {
      // Port exists but state may be stale — re-ping to refresh.
      sendCommand({ command: "ping" }).then((resp) => {
        if (!port) {
          resolve(null);
          return;
        }
        if (resp && resp.success) {
          connectionState = "ready";
          failureNotified = false;
        } else {
          connectionState = "daemon_unavailable";
        }
        resolve(port);
      });
      return;
    }
    try {
      port = chrome.runtime.connectNative(HOST_NAME);
    } catch {
      connectionState = "host_unavailable";
      resolve(null);
      return;
    }

    port.onDisconnect.addListener(() => {
      debugLog("bolt-host port disconnected", chrome.runtime.lastError?.message);
      connectionState = "host_unavailable";
      port = null;
      // Reject all pending callbacks
      for (const cb of pendingCallbacks.values()) cb(null);
      pendingCallbacks.clear();
    });

    port.onMessage.addListener((msg) => {
      // Correlate response to request by ID
      const id = msg?.id;
      if (id && pendingCallbacks.has(id)) {
        const cb = pendingCallbacks.get(id);
        pendingCallbacks.delete(id);
        cb(msg);
      }
    });

    // Ping to determine state
    sendCommand({ command: "ping" }).then((resp) => {
      // Guard: if onDisconnect fired during the ping, port is null
      // and connectionState is already "host_unavailable" — don't overwrite.
      if (!port) {
        resolve(null);
        return;
      }
      if (resp && resp.success) {
        connectionState = "ready";
        failureNotified = false;
      } else {
        connectionState = "daemon_unavailable";
      }
      resolve(port);
    });
  });  // end _initPort Promise
}  // end _initPort

const COMMAND_TIMEOUT_MS = 15000; // 15s — longer than bolt-host's 10s HTTP timeout

function sendCommand(cmd) {
  return new Promise((resolve) => {
    if (!port) {
      resolve(null);
      return;
    }
    const id = String(nextId++);
    cmd.id = id;

    const timer = setTimeout(() => {
      pendingCallbacks.delete(id);
      debugLog("bolt-host timeout", "trace=" + id);
      resolve(null); // treat timeout as failure
    }, COMMAND_TIMEOUT_MS);

    pendingCallbacks.set(id, (msg) => {
      clearTimeout(timer);
      resolve(msg);
    });
    try {
      port.postMessage(cmd);
    } catch {
      clearTimeout(timer);
      pendingCallbacks.delete(id);
      resolve(null);
    }
  });
}

async function sendWithConnection(cmd) {
  await ensurePort();
  if (connectionState === "host_unavailable") return null;
  return sendCommand(cmd);
}

// --- Context menu ---

const DEFAULT_SETTINGS = {
  captureEnabled: false,
  debugLogging: false,
  minFileSize: 10485760,  // 10 MB
  extensionWhitelist: [],
  extensionBlacklist: [],
  domainBlocklist: [],
};

chrome.runtime.onInstalled.addListener(() => {
  // Seed defaults into storage (won't overwrite existing values)
  chrome.storage.local.get(DEFAULT_SETTINGS, (existing) => {
    chrome.storage.local.set(existing);
  });

  chrome.contextMenus.create({
    id: "download-with-bolt",
    title: "Download with Bolt",
    contexts: ["link"],
  });
});

chrome.contextMenus.onClicked.addListener(async (info, tab) => {
  if (info.menuItemId !== "download-with-bolt") return;

  const url = info.linkUrl;
  if (!url) return;

  const traceID = generateTraceID();
  debugLog("context menu download", url, "trace=" + traceID);

  const headers = await collectHeaders(url, tab?.url);

  const resp = await sendWithConnectionFast({
    command: "add_download",
    data: { url, headers, referer_url: tab?.url || "", trace_id: traceID },
  });

  if (resp && resp.success) {
    const filename = resp.data?.download?.filename || url.split("/").pop();
    debugLog("download handed off successfully", "trace=" + traceID);
    showNotification("Sent to Bolt", filename);
  } else if (!resp || resp.error === "daemon_unavailable" || resp.error === "host_unavailable" || resp.error === "timeout") {
    // Bolt is genuinely unreachable — fall back to browser download
    debugLog("bolt unavailable, falling back to browser", url);
    reinitiatedUrls.add(url);
    chrome.downloads.download({ url });
    showNotification("Bolt unavailable", "Downloading normally");
  } else {
    // Daemon rejected the request (e.g. duplicate_filename) — surface the error, don't bypass
    debugLog("daemon rejected", resp.error, url, "trace=" + traceID);
    showNotification("Download rejected", resp.error || "Unknown error");
  }
});

// --- Cached settings ---

let cachedSettings = { ...DEFAULT_SETTINGS };

chrome.storage.local.get(DEFAULT_SETTINGS, (s) => {
  cachedSettings = s;
});

chrome.storage.onChanged.addListener((changes) => {
  for (const key of Object.keys(cachedSettings)) {
    if (changes[key]) {
      cachedSettings[key] = changes[key].newValue;
    }
  }
});

// --- Automatic capture ---

chrome.downloads.onCreated.addListener(async (downloadItem) => {
  const url = downloadItem.url;

  // Skip data: URLs, blob: URLs, and chrome: URLs
  if (!url || !url.startsWith("http")) return;

  // Re-interception prevention
  if (reinitiatedUrls.has(url)) {
    reinitiatedUrls.delete(url);
    return;
  }

  if (!cachedSettings.captureEnabled) {
    debugLog("capture disabled, skipping", url);
    return;
  }

  // If the originating page is blocklisted, ignore entirely.
  // Prefer referrer; fall back to active tab (best-effort — the active tab
  // may not be the one that initiated the download if the user switched tabs).
  let pageUrl = downloadItem.referrer || "";
  if (!pageUrl) {
    try {
      const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
      pageUrl = tab?.url || "";
    } catch { /* ignore */ }
  }
  if (isPageBlocked(pageUrl, cachedSettings.domainBlocklist)) {
    debugLog("page blocklisted, ignoring", url);
    return;
  }

  // Apply filters
  if (!passesFilters(url, downloadItem.totalBytes, cachedSettings)) {
    debugLog("filtered out", url);
    return;
  }

  const traceID = generateTraceID();
  debugLog("capturing download", url, "trace=" + traceID);

  const headers = await collectHeaders(url, downloadItem.referrer);

  const resp = await sendWithConnection({
    command: "add_download",
    data: {
      url,
      headers,
      referer_url: downloadItem.referrer || "",
      filename: downloadItem.filename || "",  // daemon's AddRequest accepts filename as a hint
      trace_id: traceID,
    },
  });

  if (resp && resp.success) {
    debugLog("download handed off successfully", "trace=" + traceID);
    // Bolt accepted — cancel the browser's download to avoid duplicates.
    // Best-effort: if cancel fails (already completed, etc.), no harm done.
    try {
      await chrome.downloads.cancel(downloadItem.id);
      chrome.downloads.erase({ id: downloadItem.id });
    } catch {
      debugLog("cancel failed for browser download", downloadItem.id);
      /* cancel/erase is best-effort */
    }
  } else if (!resp || resp.error === "daemon_unavailable" || resp.error === "host_unavailable" || resp.error === "timeout") {
    // Bolt is genuinely unreachable — let the browser download continue as-is
    debugLog("bolt unavailable, falling back to browser", url);
    if (!failureNotified) {
      showNotification("Bolt unavailable", "Downloading normally");
      failureNotified = true;
    }
  } else {
    // Daemon rejected the request — surface the error, let browser download continue
    debugLog("daemon rejected", resp.error, url, "trace=" + traceID);
    showNotification("Download rejected", resp.error || "Unknown error");
  }
});

// --- Content script messages ---

// Shorter timeout for user-initiated actions (link clicks, context menu)
// so the browser doesn't stall visibly if bolt-host or daemon hangs.
const USER_ACTION_TIMEOUT_MS = 3000;

function sendCommandWithTimeout(cmd, timeoutMs) {
  return new Promise((resolve) => {
    if (!port) {
      resolve(null);
      return;
    }
    const id = String(nextId++);
    cmd.id = id;

    const timer = setTimeout(() => {
      pendingCallbacks.delete(id);
      debugLog("bolt-host timeout", "trace=" + id);
      resolve(null);
    }, timeoutMs);

    pendingCallbacks.set(id, (msg) => {
      clearTimeout(timer);
      resolve(msg);
    });
    try {
      port.postMessage(cmd);
    } catch {
      clearTimeout(timer);
      pendingCallbacks.delete(id);
      resolve(null);
    }
  });
}

async function sendWithConnectionFast(cmd) {
  await ensurePort();
  if (connectionState === "host_unavailable") return null;
  return sendCommandWithTimeout(cmd, USER_ACTION_TIMEOUT_MS);
}

chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  if (msg.type !== "download-link") return;

  (async () => {
    const traceID = generateTraceID();
    debugLog("link click download", msg.url, "trace=" + traceID);

    // If the originating page is blocklisted, let browser handle it
    if (isPageBlocked(msg.pageUrl, cachedSettings.domainBlocklist)) {
      debugLog("page blocklisted, ignoring", msg.url, "trace=" + traceID);
      chrome.downloads.download({ url: msg.url });
      sendResponse({ ok: true });
      return;
    }

    // Apply the same filters as automatic capture
    if (!passesFilters(msg.url, 0, cachedSettings)) {
      debugLog("filtered out", msg.url, "trace=" + traceID);
      // Filtered out — let the browser handle it normally
      chrome.downloads.download({ url: msg.url });
      sendResponse({ ok: true });
      return;
    }

    const headers = await collectHeaders(msg.url, msg.pageUrl);

    const resp = await sendWithConnection({
      command: "add_download",
      data: { url: msg.url, headers, referer_url: msg.pageUrl || "", trace_id: traceID },
    });

    if (resp && resp.success) {
      const filename =
        resp.data?.download?.filename || msg.url.split("/").pop();
      debugLog("download handed off successfully", "trace=" + traceID);
      showNotification("Sent to Bolt", filename);
    } else if (!resp || resp.error === "daemon_unavailable" || resp.error === "host_unavailable" || resp.error === "timeout") {
      debugLog("bolt unavailable, falling back to browser", msg.url);
      reinitiatedUrls.add(msg.url);
      chrome.downloads.download({ url: msg.url });
      showNotification("Bolt unavailable", "Downloading normally");
    } else {
      debugLog("daemon rejected", resp.error, msg.url, "trace=" + traceID);
      showNotification("Download rejected", resp.error || "Unknown error");
    }

    sendResponse({ ok: true });
  })();

  return true; // async response
});

// --- Filters ---

function isPageBlocked(pageUrl, blocklist) {
  if (!blocklist.length || !pageUrl) return false;
  try {
    const domain = new URL(pageUrl).hostname;
    return blocklist.some((d) => {
      const blocked = d.trim();
      return domain === blocked || domain.endsWith("." + blocked);
    });
  } catch {
    return false;
  }
}

// Small/text file extensions that should never be intercepted
const SKIP_EXTENSIONS = new Set([
  ".html", ".htm", ".css", ".js", ".ts", ".jsx", ".tsx",
  ".json", ".xml", ".yaml", ".yml", ".toml",
  ".md", ".txt", ".csv", ".log",
  ".sh", ".bash", ".zsh", ".fish", ".bat", ".ps1",
  ".py", ".rb", ".pl", ".php", ".go", ".rs", ".java", ".kt",
  ".c", ".cpp", ".h", ".hpp", ".cs", ".swift", ".m",
  ".sql", ".graphql",
  ".conf", ".cfg", ".ini", ".env",
  ".gitignore", ".dockerignore", ".editorconfig",
]);

function passesFilters(url, totalBytes, settings) {
  // Always skip text/source/script files — too small to benefit from Bolt
  const ext = getFileExtension(url);
  if (SKIP_EXTENSIONS.has(ext)) return false;

  // Min file size (only when Chrome provides it)
  if (settings.minFileSize > 0 && totalBytes > 0) {
    if (totalBytes < settings.minFileSize) return false;
  }

  // Extension whitelist/blacklist (ext already computed above)
  if (settings.extensionWhitelist.length > 0) {
    if (!settings.extensionWhitelist.some((e) => e.trim() === ext))
      return false;
  }
  if (settings.extensionBlacklist.length > 0) {
    if (settings.extensionBlacklist.some((e) => e.trim() === ext))
      return false;
  }

  return true;
}

function getFileExtension(url) {
  try {
    const pathname = new URL(url).pathname;
    const filename = pathname.split("/").pop();
    if (!filename) return "";

    // Handle compound extensions (.tar.gz, .tar.bz2, .tar.xz)
    for (const ext of [".tar.gz", ".tar.bz2", ".tar.xz"]) {
      if (filename.toLowerCase().endsWith(ext)) return ext;
    }

    const dotIdx = filename.lastIndexOf(".");
    if (dotIdx === -1) return "";
    return filename.slice(dotIdx).toLowerCase();
  } catch {
    return "";
  }
}

// --- Headers/cookies ---

async function collectHeaders(url, pageUrl) {
  const headers = {};

  // Cookies
  try {
    const cookies = await chrome.cookies.getAll({ url });
    if (cookies.length > 0) {
      headers["Cookie"] = cookies.map((c) => `${c.name}=${c.value}`).join("; ");
    }
  } catch {
    /* cookies unavailable */
  }

  // Referrer
  if (pageUrl) {
    headers["Referer"] = pageUrl;
  }

  // User-Agent as a best-effort compatibility hint. May not exactly match
  // the UA Chrome used for the original request (can be reduced/frozen in
  // some environments), but good enough for servers that check it.
  headers["User-Agent"] = navigator.userAgent;

  return headers;
}

// --- Notifications ---

function showNotification(title, message) {
  chrome.notifications.create({
    type: "basic",
    iconUrl: "icons/icon-128.png",
    title,
    message,
  });
}

// --- State query for popup ---

chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  if (msg.type === "get-state") {
    (async () => {
      // Re-ping if state is stale
      if (connectionState !== "ready") {
        await ensurePort();
      }
      sendResponse({ connectionState });
    })();
    return true;
  }
});
