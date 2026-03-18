const HOST_NAME = "com.fhsinchy.bolt";

// --- Connection state ---
// "host_unavailable" | "daemon_unavailable" | "ready"
let connectionState = "host_unavailable";
let port = null;
let pendingCallbacks = new Map(); // id -> resolve
let nextId = 1;
let failureNotified = false;
let reinitiatedUrls = new Set();

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
      resolve(port);
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
      resolve(null); // treat timeout as failure
    }, COMMAND_TIMEOUT_MS);

    pendingCallbacks.set(id, (msg) => {
      clearTimeout(timer);
      resolve(msg);
    });
    port.postMessage(cmd);
  });
}

async function sendWithConnection(cmd) {
  await ensurePort();
  if (connectionState === "host_unavailable") return null;
  return sendCommand(cmd);
}

// --- Context menu ---

chrome.runtime.onInstalled.addListener(() => {
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

  const headers = await collectHeaders(url, tab?.url);

  const resp = await sendWithConnection({
    command: "add_download",
    data: { url, headers, referer_url: tab?.url || "" },
  });

  if (resp && resp.success) {
    const filename = resp.data?.download?.filename || url.split("/").pop();
    showNotification("Sent to Bolt", filename);
  } else {
    chrome.downloads.download({ url });
    showNotification("Bolt unavailable", "Downloading normally");
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

  // Check if capture is enabled
  const settings = await chrome.storage.local.get({
    captureEnabled: false,
    minFileSize: 0,
    extensionWhitelist: [],
    extensionBlacklist: [],
    domainBlocklist: [],
  });

  if (!settings.captureEnabled) return;

  // Apply filters
  if (!passesFilters(url, downloadItem.totalBytes, settings)) return;

  // Check connection
  if (connectionState !== "ready") {
    await ensurePort();
    if (connectionState !== "ready") return;
  }

  // Best-effort cancel + erase to avoid stale entries in Chrome download UI.
  // If cancel fails (download already completed, etc.), we continue with the
  // handoff anyway — the daemon will handle duplicates.
  try {
    await chrome.downloads.cancel(downloadItem.id);
    chrome.downloads.erase({ id: downloadItem.id });
  } catch {
    /* cancel/erase is best-effort */
  }

  const headers = await collectHeaders(url, downloadItem.referrer);

  const resp = await sendWithConnection({
    command: "add_download",
    data: {
      url,
      headers,
      referer_url: downloadItem.referrer || "",
      filename: downloadItem.filename || "",  // daemon's AddRequest accepts filename as a hint
    },
  });

  if (!resp || !resp.success) {
    // Fallback: re-initiate browser download
    reinitiatedUrls.add(url);
    chrome.downloads.download({ url });
    if (!failureNotified) {
      showNotification("Bolt unavailable", "Downloading normally");
      failureNotified = true;
    }
  }
});

// --- Content script messages ---

chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  if (msg.type !== "download-link") return;

  (async () => {
    const headers = await collectHeaders(msg.url, msg.pageUrl);

    const resp = await sendWithConnection({
      command: "add_download",
      data: { url: msg.url, headers, referer_url: msg.pageUrl || "" },
    });

    if (resp && resp.success) {
      const filename =
        resp.data?.download?.filename || msg.url.split("/").pop();
      showNotification("Sent to Bolt", filename);
    } else {
      chrome.downloads.download({ url: msg.url });
      showNotification("Bolt unavailable", "Downloading normally");
    }

    sendResponse({ ok: true });
  })();

  return true; // async response
});

// --- Filters ---

function passesFilters(url, totalBytes, settings) {
  // Min file size (only when Chrome provides it)
  if (settings.minFileSize > 0 && totalBytes > 0) {
    if (totalBytes < settings.minFileSize) return false;
  }

  // Domain blocklist
  if (settings.domainBlocklist.length > 0) {
    try {
      const domain = new URL(url).hostname;
      if (settings.domainBlocklist.some((d) => domain.endsWith(d.trim())))
        return false;
    } catch {
      /* invalid URL, let it through */
    }
  }

  // Extension whitelist/blacklist
  const ext = getFileExtension(url);
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
    const last = pathname.split("/").pop();
    const dotIdx = last.lastIndexOf(".");
    if (dotIdx === -1) return "";
    return last.slice(dotIdx).toLowerCase();
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
