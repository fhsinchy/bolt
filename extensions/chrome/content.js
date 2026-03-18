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
  ".deb", ".rpm", ".AppImage", ".flatpak", ".snap",
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

// Cache capture state so we can check synchronously in the click handler.
// Updated on storage changes and on initial load.
let captureEnabled = false;
chrome.storage.local.get({ captureEnabled: false }, (s) => {
  captureEnabled = s.captureEnabled;
});
chrome.storage.onChanged.addListener((changes) => {
  if (changes.captureEnabled) {
    captureEnabled = changes.captureEnabled.newValue;
  }
});

document.addEventListener("click", (e) => {
  // Only intercept left clicks without modifiers
  if (e.button !== 0 || e.ctrlKey || e.shiftKey || e.altKey || e.metaKey) return;
  if (!captureEnabled) return;

  const link = e.target.closest("a[href]");
  if (!link) return;

  const url = link.href;
  if (!isDownloadLink(url)) return;

  // preventDefault must be synchronous — no awaits before this point.
  e.preventDefault();
  e.stopPropagation();

  chrome.runtime.sendMessage({
    type: "download-link",
    url,
    pageUrl: window.location.href,
  });
}, true);
