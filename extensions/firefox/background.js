// Bolt Capture — Background Script (Firefox)
// Intercepts browser downloads and sends them to the Bolt daemon.
//
// Dev logging: open about:debugging → This Firefox → Bolt Capture → Inspect
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
const BLOCKED_DOMAINS = ['localhost', '127.0.0.1', '[::1]'];

// File extensions to never intercept (web resources)
const BLOCKED_EXTENSIONS = ['.html', '.htm', '.json', '.xml', '.js', '.css'];

// Track URLs we re-initiated so we don't intercept them again (infinite loop guard).
const redownloadUrls = new Set();

// --- Logging ---

function log(...args) {
  console.log('[Bolt]', ...args);
}

function warn(...args) {
  console.warn('[Bolt]', ...args);
}

// --- Config helpers ---

async function getConfig() {
  const result = await browser.storage.local.get('config');
  return { ...DEFAULT_CONFIG, ...result.config };
}

// --- Cookie helpers ---

async function getCookiesForUrl(url) {
  try {
    const cookies = await browser.cookies.getAll({ url });
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

  log('POST /api/downloads', JSON.stringify(body));

  const resp = await fetch(`${config.serverUrl}/api/downloads`, {
    method: 'POST',
    headers,
    body: JSON.stringify(body),
  });

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
  if (BLOCKED_DOMAINS.includes(parsed.hostname)) return false;

  // Skip blocked extensions (hardcoded, non-negotiable)
  const path = parsed.pathname.toLowerCase();
  for (const ext of BLOCKED_EXTENSIONS) {
    if (path.endsWith(ext)) return false;
  }

  // Skip data: and blob: URLs
  if (parsed.protocol === 'data:' || parsed.protocol === 'blob:') return false;

  // User domain blocklist (with subdomain matching)
  const domainBlocklist = config.domainBlocklist || [];
  const hostname = parsed.hostname.toLowerCase();
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
  browser.notifications.create({
    type: 'basic',
    iconUrl: 'icons/icon-128.png',
    title,
    message,
  });
}

function showSuccess(title, message) {
  browser.notifications.create({
    type: 'basic',
    iconUrl: 'icons/icon-128.png',
    title,
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

// --- Build headers for Bolt ---

function buildDownloadHeaders(cookieString, referrerUrl, userAgent) {
  const headers = {};
  if (cookieString) headers['Cookie'] = cookieString;
  if (referrerUrl) headers['Referer'] = referrerUrl;
  if (userAgent) headers['User-Agent'] = userAgent;
  return headers;
}

// --- Message handler (content script link interception) ---

browser.runtime.onMessage.addListener((msg, sender) => {
  if (msg.type === 'open-settings') {
    browser.tabs.create({ url: 'about:preferences#general' });
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
      browser.downloads.download({ url });
      return;
    }

    const { ok, reason } = await checkBoltReachable(config.serverUrl, config.authToken);
    if (!ok) {
      log('Bolt not reachable (' + reason + '), falling back to browser download');
      notifyUnreachable(reason);
      redownloadUrls.add(url);
      browser.downloads.download({ url });
      return;
    }

    const cookieString = await getCookiesForUrl(url);
    const downloadHeaders = buildDownloadHeaders(cookieString, referrer, navigator.userAgent);

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
        await sendToBolt(config, body);
        showSuccess('Bolt Capture', `Sent to Bolt: ${filename || url}`);
      }
    } catch (err) {
      warn('Send to Bolt failed, falling back to browser download:', err.message);
      showError('Bolt Capture', `Failed: ${err.message}`);
      redownloadUrls.add(url);
      browser.downloads.download({ url });
    }
  })();
});

// --- Context menu ---

browser.runtime.onInstalled.addListener(({ reason }) => {
  log('Extension installed, registering context menu');
  browser.menus.create({
    id: 'download-with-bolt',
    title: 'Download with Bolt',
    contexts: ['link'],
  });

  if (reason === 'install') {
    browser.tabs.create({ url: browser.runtime.getURL('welcome/welcome.html') });
  }
});

browser.menus.onClicked.addListener(async (info, tab) => {
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
      await sendToBolt(config, body);
      showSuccess('Bolt Capture', `Sent to Bolt: ${filename || url}`);
    }
  } catch (err) {
    warn('Context menu send failed:', err.message);
    showError('Bolt Capture', `Failed: ${err.message}`);
  }
});

// --- Download interception (fallback for non-click downloads) ---

browser.downloads.onCreated.addListener(async (downloadItem) => {
  const url = downloadItem.finalUrl || downloadItem.url;

  // Skip downloads we re-initiated after a failed Bolt handoff.
  if (redownloadUrls.has(url)) {
    redownloadUrls.delete(url);
    log('Skipping re-initiated download:', url);
    return;
  }

  const config = await getConfig();
  if (!config.captureEnabled) return;

  if (!shouldCapture(url, config)) {
    log('Skipping (filtered):', url);
    return;
  }

  // Min file size filter
  if (config.minFileSize > 0 && downloadItem.totalBytes > 0 && downloadItem.totalBytes < config.minFileSize) {
    log('Skipping (below min size):', url, downloadItem.totalBytes, '<', config.minFileSize);
    return;
  }

  log('Intercepted download:', url);

  // Cancel the browser download IMMEDIATELY to suppress the save dialog / download bar.
  // We check Bolt reachability after; if Bolt is down we re-initiate the browser download.
  try {
    await browser.downloads.cancel(downloadItem.id);
    await browser.downloads.erase({ id: downloadItem.id });
  } catch {
    // Download may have already completed or been removed
    warn('Could not cancel browser download (already completed?)');
    return;
  }

  log('Browser download cancelled, checking Bolt');

  // Now verify Bolt is reachable. If not, give the download back to the browser.
  const { ok, reason } = await checkBoltReachable(config.serverUrl, config.authToken);
  if (!ok) {
    log('Bolt not reachable (' + reason + '), re-initiating browser download');
    notifyUnreachable(reason);
    redownloadUrls.add(url);
    browser.downloads.download({ url });
    return;
  }

  // Gather cookies and headers
  const cookieString = await getCookiesForUrl(url);
  const referrer = downloadItem.referrer || '';
  const downloadHeaders = buildDownloadHeaders(cookieString, referrer, navigator.userAgent);

  const filename = downloadItem.filename
    ? downloadItem.filename.split('/').pop().split('\\').pop()
    : '';

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
      await sendToBolt(config, body);
      showSuccess('Bolt Capture', `Sent to Bolt: ${filename || url}`);
    }
  } catch (err) {
    warn('Send to Bolt failed, re-initiating browser download:', err.message);
    showError('Bolt Capture', `Failed to send to Bolt: ${err.message}`);
    // Fall back to browser download so the user doesn't lose the file.
    redownloadUrls.add(url);
    browser.downloads.download({ url });
  }
});
