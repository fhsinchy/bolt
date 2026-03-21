// Content script: intercept clicks on download links when capture is enabled.
// Injected into all pages via manifest.json content_scripts declaration.
//
// This is intentionally conservative — it only matches URLs with file extensions
// in the path. URLs with filenames in query parameters or no extension at all
// will not be caught here. That is fine: the context menu and downloads.onCreated
// paths are the primary capture mechanisms. This script is a convenience for
// explicit link clicks on obvious download links.

const DOWNLOAD_EXTENSIONS = new Set([
  ".zip", ".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tar.xz",
  ".gz", ".bz2", ".xz", ".7z", ".rar",
  ".iso", ".img",
  ".deb", ".rpm", ".appimage", ".flatpak", ".snap",
  ".exe", ".msi", ".dmg", ".pkg",
  ".pdf",
  ".bin", ".run", ".sh",
]);

function getExtension(url) {
  try {
    const pathname = new URL(url).pathname;
    const filename = pathname.split("/").pop();
    if (!filename) return "";

    // Handle .tar.gz, .tar.bz2, .tar.xz
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

function isDownloadLink(url) {
  if (!url || !url.startsWith("http")) return false;
  const ext = getExtension(url);
  return DOWNLOAD_EXTENSIONS.has(ext);
}

// Cache settings so we can check synchronously in the click handler.
// Updated on storage changes and on initial load.
let cachedSettings = {
  captureEnabled: false,
  debugLogging: false,
  domainBlocklist: [],
  extensionWhitelist: [],
  extensionBlacklist: [],
};

function debugLog(...args) {
  if (cachedSettings.debugLogging) {
    console.log("[bolt]", ...args);
  }
}

chrome.storage.local.get(cachedSettings, (s) => {
  Object.assign(cachedSettings, s);
});
chrome.storage.onChanged.addListener((changes) => {
  for (const key of Object.keys(cachedSettings)) {
    if (changes[key]) {
      cachedSettings[key] = changes[key].newValue;
    }
  }
});

function passesDomainFilter(url) {
  if (cachedSettings.domainBlocklist.length === 0) return true;
  try {
    const domain = new URL(url).hostname;
    return !cachedSettings.domainBlocklist.some((d) => {
      const blocked = d.trim();
      return domain === blocked || domain.endsWith("." + blocked);
    });
  } catch {
    return true;
  }
}

function passesExtensionFilter(url) {
  const ext = getExtension(url);
  if (cachedSettings.extensionWhitelist.length > 0) {
    if (!cachedSettings.extensionWhitelist.some((e) => e.trim() === ext)) return false;
  }
  if (cachedSettings.extensionBlacklist.length > 0) {
    if (cachedSettings.extensionBlacklist.some((e) => e.trim() === ext)) return false;
  }
  return true;
}

document.addEventListener("click", (e) => {
  // Only intercept left clicks without modifiers
  if (e.button !== 0 || e.ctrlKey || e.shiftKey || e.altKey || e.metaKey) return;
  if (!cachedSettings.captureEnabled) return;

  const link = e.target.closest("a[href]");
  if (!link) return;

  const url = link.href;
  if (!isDownloadLink(url)) {
    debugLog("click on non-download link", url);
    return;
  }

  // Check filters synchronously before preventing default — if filtered,
  // let the browser handle the click normally.
  if (!passesDomainFilter(url) || !passesExtensionFilter(url)) {
    debugLog("click filtered out", url);
    return;
  }

  // preventDefault must be synchronous — no awaits before this point.
  // Only preventDefault — do NOT stopPropagation, because the page's own
  // click handlers still need to fire (e.g. Mediafire updates button UI).
  e.preventDefault();
  debugLog("click intercepted, sending to background", url);

  chrome.runtime.sendMessage({
    type: "download-link",
    url,
    pageUrl: window.location.href,
  });
}, true);
