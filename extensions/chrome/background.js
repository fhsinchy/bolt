// Bolt Capture — Service Worker (Chrome)
// Intercepts browser downloads and sends them to the Bolt daemon.
//
// Dev logging: open chrome://extensions → Bolt Capture → "Service worker" link
// to see console output from this file.

const DEFAULT_CONFIG = {
  serverUrl: 'http://127.0.0.1:9683',
  authToken: '',
  captureEnabled: true,
  minFileSize: 0,           // bytes, 0=disabled
  extensionWhitelist: [],   // e.g. ['.zip', '.iso']
  extensionBlacklist: [],   // e.g. ['.exe']
  domainBlocklist: [],      // e.g. ['ads.example.com']
};

// Domains to never intercept (local development, etc.)
const BLOCKED_DOMAINS = ['localhost', '127.0.0.1', '[::1]', '1fichier.com'];

// Domains where we show a notification explaining why we skipped
const INCOMPATIBLE_DOMAINS = ['1fichier.com'];

// File extensions to never intercept (web resources)
const BLOCKED_EXTENSIONS = ['.html', '.htm', '.json', '.xml', '.js', '.css'];

// Track URLs we re-initiated so we don't intercept them again (infinite loop guard).
const redownloadUrls = new Set();

// URLs detected via Content-Disposition headers (webRequest). Used as a fallback
// when onCreated doesn't fire for navigation-to-download conversions.
// Maps URL → { tabId, timestamp }
const pendingCaptures = new Map();

// Timestamp when this service worker session started. Used to ignore downloads
// that Chrome resumes/retries from a previous session on browser restart.
const serviceWorkerStartTime = Date.now();

// --- Logging ---

function log(...args) {
  console.log('[Bolt]', ...args);
}

function warn(...args) {
  console.warn('[Bolt]', ...args);
}

// --- Config helpers ---

async function getConfig() {
  const result = await chrome.storage.local.get('config');
  return { ...DEFAULT_CONFIG, ...result.config };
}

// --- Download UI suppression ---

async function syncDownloadUi() {
  const config = await getConfig();
  const enabled = !config.captureEnabled; // UI disabled when capture is ON
  try {
    await chrome.downloads.setUiOptions({ enabled });
    log('Download UI', enabled ? 'enabled' : 'disabled');
  } catch (err) {
    warn('setUiOptions failed:', err.message);
  }
}

// Sync on service worker startup
syncDownloadUi();

// Re-sync when config changes (e.g. capture toggle in popup)
chrome.storage.onChanged.addListener((changes, area) => {
  if (area === 'local' && changes.config) {
    syncDownloadUi();
  }
});

// --- Cookie helpers ---

async function getCookiesForUrl(url) {
  try {
    const cookies = await chrome.cookies.getAll({ url });
    if (!cookies || cookies.length === 0) return '';
    return cookies.map((c) => `${c.name}=${c.value}`).join('; ');
  } catch {
    return '';
  }
}

// --- Bolt API helpers ---

async function checkBoltReachable(serverUrl, token) {
  try {
    const headers = {};
    if (token) headers['Authorization'] = `Bearer ${token}`;

    const resp = await fetch(`${serverUrl}/api/stats`, {
      method: 'GET',
      headers,
      signal: AbortSignal.timeout(3000),
    });

    if (resp.status === 401) {
      warn('Bolt auth failed:', resp.status, resp.statusText);
      return { ok: false, reason: 'auth_failed' };
    }

    if (!resp.ok) {
      warn('Bolt reachability check failed:', resp.status, resp.statusText);
      return { ok: false, reason: 'unreachable' };
    }

    return { ok: true, reason: 'reachable' };
  } catch (err) {
    warn('Bolt not reachable:', err.message);
    return { ok: false, reason: 'unreachable' };
  }
}

async function sendToBolt(config, body) {
  const headers = { 'Content-Type': 'application/json' };
  if (config.authToken) headers['Authorization'] = `Bearer ${config.authToken}`;

  log('POST /api/downloads', body.url);

  const resp = await fetch(`${config.serverUrl}/api/downloads`, {
    method: 'POST',
    headers,
    body: JSON.stringify(body),
  });

  if (resp.status === 409) {
    const data = await resp.json().catch(() => ({}));
    if (data.code === 'DUPLICATE_FILENAME') {
      return data;
    }
  }

  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
    throw new Error(err.error || `HTTP ${resp.status}`);
  }
  return resp.json();
}

async function refreshDownload(config, id, url, downloadHeaders) {
  const headers = { 'Content-Type': 'application/json' };
  if (config.authToken) headers['Authorization'] = `Bearer ${config.authToken}`;

  log('POST /api/downloads/' + id + '/refresh', { url });

  const resp = await fetch(`${config.serverUrl}/api/downloads/${id}/refresh`, {
    method: 'POST',
    headers,
    body: JSON.stringify({ url, headers: downloadHeaders }),
  });

  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
    throw new Error(err.error || `HTTP ${resp.status}`);
  }
  return resp.json();
}

// --- Filtering ---

function shouldCapture(url, config) {
  if (!url) return false;

  let parsed;
  try {
    parsed = new URL(url);
  } catch {
    return false;
  }

  // Skip blocked domains (hardcoded, non-negotiable)
  const hostname = parsed.hostname.toLowerCase();
  for (const domain of BLOCKED_DOMAINS) {
    if (hostname === domain || hostname.endsWith('.' + domain)) return false;
  }

  // Skip blocked extensions (hardcoded, non-negotiable)
  const path = parsed.pathname.toLowerCase();
  for (const ext of BLOCKED_EXTENSIONS) {
    if (path.endsWith(ext)) return false;
  }

  // Skip data: and blob: URLs
  if (parsed.protocol === 'data:' || parsed.protocol === 'blob:') return false;

  // User domain blocklist (with subdomain matching)
  const domainBlocklist = config.domainBlocklist || [];
  for (const domain of domainBlocklist) {
    if (hostname === domain || hostname.endsWith('.' + domain)) return false;
  }

  // User extension blacklist
  const extBlacklist = config.extensionBlacklist || [];
  for (const ext of extBlacklist) {
    if (path.endsWith(ext)) return false;
  }

  // User extension whitelist (if non-empty, only capture matching extensions)
  const extWhitelist = config.extensionWhitelist || [];
  if (extWhitelist.length > 0) {
    let matches = false;
    for (const ext of extWhitelist) {
      if (path.endsWith(ext)) {
        matches = true;
        break;
      }
    }
    if (!matches) return false;
  }

  return true;
}

// --- Refresh matching ---

async function findRefreshCandidate(config, url, filename) {
  try {
    const headers = {};
    if (config.authToken) headers['Authorization'] = `Bearer ${config.authToken}`;

    const resp = await fetch(`${config.serverUrl}/api/downloads?status=refresh`, {
      method: 'GET',
      headers,
    });

    if (!resp.ok) return null;

    const data = await resp.json();
    const downloads = data.downloads || [];

    if (downloads.length === 0) return null;

    // Match by filename (exact)
    for (const dl of downloads) {
      if (dl.filename === filename) return dl;
    }

    // Match by domain + path similarity
    let parsed;
    try {
      parsed = new URL(url);
    } catch {
      return null;
    }

    for (const dl of downloads) {
      try {
        const dlParsed = new URL(dl.url);
        if (dlParsed.hostname === parsed.hostname) return dl;
      } catch {
        continue;
      }
    }

    return null;
  } catch {
    return null;
  }
}

// --- Notifications ---

function showError(title, message) {
  chrome.notifications.create({
    type: 'basic',
    iconUrl: 'icons/icon-128.png',
    title: 'Error: ' + title,
    message,
  });
}

function showSuccess(title, message) {
  chrome.notifications.create({
    type: 'basic',
    iconUrl: 'icons/icon-128.png',
    title: 'Success: ' + title,
    message,
  });
}

function notifyUnreachable(reason) {
  if (reason === 'auth_failed') {
    showError('Bolt Capture', 'Bolt rejected the access key — check extension settings');
  } else {
    showError('Bolt Capture', 'Bolt is not running — download sent to browser instead');
  }
}

// --- Filename extraction from URL ---

// Mirrors the Go-side filenameFromURL logic: prefer the URL path segment if
// it has a file extension, otherwise check query parameters (filename, file,
// name, fname). Skip path segments that are very long with no extension
// (CDN hashes/tokens).
function filenameFromURL(url) {
  try {
    const parsed = new URL(url);
    const segments = parsed.pathname.split('/');
    const lastSeg = decodeURIComponent(segments[segments.length - 1] || '');

    // If the path segment has a file extension, use it.
    if (lastSeg && lastSeg.includes('.')) {
      return lastSeg;
    }

    // Check query parameters for filename hints.
    for (const param of ['filename', 'file', 'name', 'fname']) {
      const val = parsed.searchParams.get(param);
      if (val) return val;
    }

    // Skip long hash-like segments (>80 chars, no extension).
    if (lastSeg && lastSeg.length <= 80) {
      return lastSeg;
    }

    return '';
  } catch {
    return '';
  }
}

// --- Build headers for Bolt ---

function buildDownloadHeaders(cookieString, referrerUrl, userAgent) {
  const headers = {};
  if (cookieString) headers['Cookie'] = cookieString;
  if (referrerUrl) headers['Referer'] = referrerUrl;
  if (userAgent) headers['User-Agent'] = userAgent;
  return headers;
}

// --- Message handler (content script link interception) ---

chrome.runtime.onMessage.addListener((msg, sender) => {
  if (msg.type === 'open-settings') {
    chrome.tabs.create({ url: 'chrome://settings/downloads' });
    return;
  }

  if (msg.type !== 'link-download') return;

  // Handle async in a self-invoking async function (listeners must return synchronously).
  (async () => {
    const url = msg.url;
    const referrer = msg.referrer || '';

    log('Content script intercepted link:', url);

    const config = await getConfig();
    if (!config.captureEnabled) {
      log('Capture disabled, falling back to browser download');
      redownloadUrls.add(url);
      chrome.downloads.download({ url });
      return;
    }

    const { ok, reason } = await checkBoltReachable(config.serverUrl, config.authToken);
    if (!ok) {
      log('Bolt not reachable (' + reason + '), falling back to browser download');
      notifyUnreachable(reason);
      await chrome.downloads.setUiOptions({ enabled: true });
      redownloadUrls.add(url);
      chrome.downloads.download({ url });
      return;
    }

    const cookieString = await getCookiesForUrl(url);
    const downloadHeaders = buildDownloadHeaders(cookieString, referrer, navigator.userAgent);

    const filename = filenameFromURL(url);

    try {
      const candidate = await findRefreshCandidate(config, url, filename);
      if (candidate) {
        log('Refresh match found:', candidate.id, candidate.filename);
        await refreshDownload(config, candidate.id, url, downloadHeaders);
        showSuccess('Bolt Capture', `Refreshed: ${candidate.filename}`);
      } else {
        const body = { url, headers: downloadHeaders };
        if (filename) body.filename = filename;
        if (referrer) body.referer_url = referrer;
        const result = await sendToBolt(config, body);
        if (result?.code === 'DUPLICATE_FILENAME') {
          showSuccess('Bolt Capture', 'Duplicate detected — check Bolt window');
        } else {
          showSuccess('Bolt Capture', `Sent to Bolt: ${filename || url}`);
        }
      }
    } catch (err) {
      warn('Send to Bolt failed, falling back to browser download:', err.message);
      showError('Bolt Capture', `Failed: ${err.message}`);
      await chrome.downloads.setUiOptions({ enabled: true });
      redownloadUrls.add(url);
      chrome.downloads.download({ url });
    }
  })();
});

// --- Context menu ---

chrome.runtime.onInstalled.addListener(({ reason }) => {
  log('Extension installed, registering context menu');
  chrome.contextMenus.create({
    id: 'download-with-bolt',
    title: 'Download with Bolt',
    contexts: ['link'],
  });

  if (reason === 'install') {
    chrome.tabs.create({ url: chrome.runtime.getURL('welcome/welcome.html') });
  }
});

chrome.contextMenus.onClicked.addListener(async (info, tab) => {
  if (info.menuItemId !== 'download-with-bolt') return;

  const url = info.linkUrl;
  if (!url) return;

  log('Context menu: Download with Bolt', url);

  const config = await getConfig();
  if (!config.captureEnabled) {
    log('Capture disabled, ignoring');
    return;
  }

  const { ok, reason } = await checkBoltReachable(config.serverUrl, config.authToken);
  if (!ok) {
    if (reason === 'auth_failed') {
      showError('Bolt Capture', 'Bolt rejected the access key — check extension settings');
    } else {
      showError('Bolt Capture', 'Bolt is not running. Cannot send download.');
    }
    return;
  }

  const cookieString = await getCookiesForUrl(url);
  const referrer = info.pageUrl || (tab && tab.url) || '';
  const downloadHeaders = buildDownloadHeaders(cookieString, referrer, navigator.userAgent);

  // Extract filename from URL
  let filename = '';
  try {
    const parsed = new URL(url);
    const parts = parsed.pathname.split('/');
    filename = decodeURIComponent(parts[parts.length - 1] || '');
  } catch {
    // ignore
  }

  try {
    // Check for refresh candidate
    const candidate = await findRefreshCandidate(config, url, filename);
    if (candidate) {
      log('Refresh match found:', candidate.id, candidate.filename);
      await refreshDownload(config, candidate.id, url, downloadHeaders);
      showSuccess('Bolt Capture', `Refreshed: ${candidate.filename}`);
    } else {
      const body = { url, headers: downloadHeaders };
      if (filename) body.filename = filename;
      if (referrer) body.referer_url = referrer;
      const result = await sendToBolt(config, body);
      if (result?.code === 'DUPLICATE_FILENAME') {
        showSuccess('Bolt Capture', 'Duplicate detected — check Bolt window');
      } else {
        showSuccess('Bolt Capture', `Sent to Bolt: ${filename || url}`);
      }
    }
  } catch (err) {
    warn('Context menu send failed:', err.message);
    showError('Bolt Capture', `Failed: ${err.message}`);
  }
});

// --- Content-Disposition detection via webRequest ---
// Detects downloads at the network level. When a navigation response has
// Content-Disposition: attachment, Chrome may convert the navigation to a
// download. onCreated should fire for these, but as a fallback (in case it
// doesn't), we detect them here and handle after a timeout.

chrome.webRequest.onHeadersReceived.addListener(
  (details) => {
    if (details.type !== 'main_frame') return;

    // Skip redirect responses — only capture final (2xx) responses.
    // A 302 redirect may include Content-Disposition headers, but the
    // download is triggered by the final response, not the redirect.
    // Without this check, the pre-redirect URL lingers in pendingCaptures
    // and causes a duplicate capture after the 3s timeout.
    if (details.statusCode >= 300 && details.statusCode < 400) return;

    const contentDisp = details.responseHeaders?.find(
      (h) => h.name.toLowerCase() === 'content-disposition',
    );
    if (!contentDisp) return;

    const value = contentDisp.value.toLowerCase();
    if (!value.startsWith('attachment') && !value.includes('filename=')) return;

    const url = details.url;
    log('Detected Content-Disposition download via headers:', url);

    pendingCaptures.set(url, {
      tabId: details.tabId,
      timestamp: Date.now(),
    });

    // If onCreated doesn't handle this within 3 seconds, handle directly
    setTimeout(() => {
      const entry = pendingCaptures.get(url);
      if (entry) {
        pendingCaptures.delete(url);
        log('onCreated did not fire, handling Content-Disposition download directly:', url);
        handleDirectCapture(url, entry);
      }
    }, 3000);
  },
  { urls: ['<all_urls>'] },
  ['responseHeaders'],
);

async function handleDirectCapture(url, entry) {
  if (redownloadUrls.has(url)) return;

  const config = await getConfig();
  if (!config.captureEnabled) return;
  if (!shouldCapture(url, config)) return;

  const { ok, reason } = await checkBoltReachable(config.serverUrl, config.authToken);
  if (!ok) {
    notifyUnreachable(reason);
    return;
  }

  const cookieString = await getCookiesForUrl(url);
  const downloadHeaders = buildDownloadHeaders(cookieString, '', navigator.userAgent);

  let filename = '';
  try {
    const parsed = new URL(url);
    const parts = parsed.pathname.split('/');
    filename = decodeURIComponent(parts[parts.length - 1] || '');
  } catch {
    // ignore
  }

  try {
    const candidate = await findRefreshCandidate(config, url, filename);
    if (candidate) {
      log('Refresh match found:', candidate.id, candidate.filename);
      await refreshDownload(config, candidate.id, url, downloadHeaders);
      showSuccess('Bolt Capture', `Refreshed: ${candidate.filename}`);
    } else {
      const body = { url, headers: downloadHeaders };
      if (filename) body.filename = filename;
      const result = await sendToBolt(config, body);
      if (result?.code === 'DUPLICATE_FILENAME') {
        showSuccess('Bolt Capture', 'Duplicate detected — check Bolt window');
      } else {
        showSuccess('Bolt Capture', `Sent to Bolt: ${filename || url}`);
      }
    }

    // Try to cancel the browser download after the fact
    try {
      const downloads = await chrome.downloads.search({ url, state: 'in_progress' });
      for (const dl of downloads) {
        await chrome.downloads.cancel(dl.id);
        await chrome.downloads.erase({ id: dl.id });
      }
    } catch {
      // Browser download may have completed — user may need to delete it manually
    }
  } catch (err) {
    warn('Direct capture failed:', err.message);
    showError('Bolt Capture', `Failed: ${err.message}`);
  }
}

// --- Download interception (fallback for non-click downloads) ---

chrome.downloads.onCreated.addListener(async (downloadItem) => {
  const url = downloadItem.finalUrl || downloadItem.url;

  // Skip downloads we re-initiated after a failed Bolt handoff.
  if (redownloadUrls.has(url)) {
    redownloadUrls.delete(url);
    log('Skipping re-initiated download:', url);
    // Restore UI suppression after the fallback download starts
    syncDownloadUi();
    return;
  }

  // Skip downloads that started before this service worker session (browser restart resumes).
  if (downloadItem.startTime) {
    const startMs = new Date(downloadItem.startTime).getTime();
    if (startMs < serviceWorkerStartTime) {
      log('Skipping (pre-existing download from previous session):', url);
      return;
    }
  }

  const config = await getConfig();
  if (!config.captureEnabled) return;

  if (!shouldCapture(url, config)) {
    log('Skipping (filtered):', url);
    pendingCaptures.delete(url);
    return;
  }

  // Skip image MIME types (user is viewing, not downloading)
  if (downloadItem.mime && downloadItem.mime.startsWith('image/')) {
    log('Skipping (image MIME type):', url, downloadItem.mime);
    pendingCaptures.delete(url);
    return;
  }

  // Min file size filter
  if (config.minFileSize > 0 && downloadItem.totalBytes > 0 && downloadItem.totalBytes < config.minFileSize) {
    log('Skipping (below min size):', url, downloadItem.totalBytes, '<', config.minFileSize);
    pendingCaptures.delete(url);
    return;
  }

  // Clear from pending captures — onCreated is handling it.
  // Clear both finalUrl and the original url to handle redirect chains
  // where onHeadersReceived stored the pre-redirect URL.
  pendingCaptures.delete(url);
  if (downloadItem.url && downloadItem.url !== url) {
    pendingCaptures.delete(downloadItem.url);
  }

  log('Intercepted download:', url);

  // Cancel the browser download IMMEDIATELY to suppress the save dialog / download bar.
  // We check Bolt reachability after; if Bolt is down we re-initiate the browser download.
  let cancelFailed = false;
  try {
    await chrome.downloads.cancel(downloadItem.id);
    await chrome.downloads.erase({ id: downloadItem.id });
  } catch {
    // Download may have already completed or is not cancellable.
    // Continue anyway — send to Bolt even if browser also downloads.
    warn('Could not cancel browser download — will send to Bolt anyway');
    cancelFailed = true;
  }

  log(cancelFailed ? 'Could not cancel browser download, checking Bolt' : 'Browser download cancelled, checking Bolt');

  // Now verify Bolt is reachable. If not, give the download back to the browser.
  const { ok, reason } = await checkBoltReachable(config.serverUrl, config.authToken);
  if (!ok) {
    log('Bolt not reachable (' + reason + '), re-initiating browser download');
    notifyUnreachable(reason);
    if (!cancelFailed) {
      await chrome.downloads.setUiOptions({ enabled: true });
      redownloadUrls.add(url);
      chrome.downloads.download({ url });
    }
    return;
  }

  // Gather cookies and headers
  const cookieString = await getCookiesForUrl(url);
  const referrer = downloadItem.referrer || '';
  const downloadHeaders = buildDownloadHeaders(cookieString, referrer, navigator.userAgent);

  let filename = downloadItem.filename
    ? downloadItem.filename.split('/').pop().split('\\').pop()
    : '';

  // Fall back to extracting filename from URL (downloadItem.filename is often
  // empty in onCreated because Chrome hasn't resolved it yet).
  if (!filename) {
    filename = filenameFromURL(url);
  }

  try {
    // Check for refresh candidate
    const candidate = await findRefreshCandidate(config, url, filename);
    if (candidate) {
      log('Refresh match found:', candidate.id, candidate.filename);
      await refreshDownload(config, candidate.id, url, downloadHeaders);
      showSuccess('Bolt Capture', `Refreshed: ${candidate.filename}`);
    } else {
      const body = { url, headers: downloadHeaders };
      if (filename) body.filename = filename;
      if (referrer) body.referer_url = referrer;
      const result = await sendToBolt(config, body);
      if (result?.code === 'DUPLICATE_FILENAME') {
        showSuccess('Bolt Capture', 'Duplicate detected — check Bolt window');
      } else {
        showSuccess('Bolt Capture', `Sent to Bolt: ${filename || url}`);
      }
    }
  } catch (err) {
    warn('Send to Bolt failed:', err.message);
    showError('Bolt Capture', `Failed to send to Bolt: ${err.message}`);
    if (!cancelFailed) {
      // Fall back to browser download so the user doesn't lose the file.
      await chrome.downloads.setUiOptions({ enabled: true });
      redownloadUrls.add(url);
      chrome.downloads.download({ url });
    }
  }
});
