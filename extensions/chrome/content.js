// Bolt Capture — Content Script (Chrome)
// Intercepts clicks on download links BEFORE the browser starts the download,
// preventing the Save As dialog from appearing.
//
// Dev logging: open DevTools on any page → Console → filter by "[Bolt]".

// File extensions that indicate a downloadable file.
const DOWNLOAD_EXTENSIONS = [
  // Archives
  '.zip', '.tar', '.gz', '.bz2', '.xz', '.7z', '.rar', '.zst',
  // Disk images
  '.iso', '.img', '.dmg',
  // Programs
  '.exe', '.msi', '.deb', '.rpm', '.appimage', '.snap', '.flatpak',
  // Documents
  '.pdf', '.doc', '.docx', '.xls', '.xlsx', '.ppt', '.pptx', '.odt', '.ods', '.odp',
  // Media
  '.mp4', '.mkv', '.avi', '.mov', '.wmv', '.flv', '.webm', '.m4v',
  '.mp3', '.flac', '.wav', '.aac', '.ogg', '.wma', '.m4a',
  // Images (large)
  '.png', '.jpg', '.jpeg', '.gif', '.bmp', '.svg', '.webp', '.tiff',
  // Torrents
  '.torrent',
];

// Extensions to never intercept (web pages / resources).
const SKIP_EXTENSIONS = ['.html', '.htm', '.php', '.asp', '.aspx', '.jsp', '.json', '.xml', '.js', '.css'];

function getExtension(url) {
  try {
    const pathname = new URL(url).pathname.toLowerCase();
    const dot = pathname.lastIndexOf('.');
    if (dot === -1) return '';
    // Strip query-like suffixes that snuck into pathname
    return pathname.slice(dot).split(/[?#]/)[0];
  } catch {
    return '';
  }
}

function isDownloadLink(anchor) {
  // Explicit download attribute is a strong signal.
  if (anchor.hasAttribute('download')) return true;

  const href = anchor.href;
  if (!href) return false;

  // Skip non-HTTP links.
  if (!href.startsWith('http://') && !href.startsWith('https://')) return false;

  // Skip same-page anchors.
  if (href.includes('#') && href.split('#')[0] === location.href.split('#')[0]) return false;

  const ext = getExtension(href);

  // Explicitly skip web page extensions.
  if (SKIP_EXTENSIONS.includes(ext)) return false;

  // Match known download extensions.
  if (DOWNLOAD_EXTENSIONS.includes(ext)) return true;

  return false;
}

document.addEventListener('click', (e) => {
  // Don't interfere with modified clicks (new tab, etc.).
  if (e.ctrlKey || e.shiftKey || e.metaKey || e.altKey || e.button !== 0) return;

  const anchor = e.target.closest('a[href]');
  if (!anchor) return;
  if (!isDownloadLink(anchor)) return;

  const url = anchor.href;

  // Ask the background script whether capture is enabled and Bolt is reachable.
  // We preventDefault synchronously so the browser never starts the download.
  e.preventDefault();
  e.stopImmediatePropagation();

  console.log('[Bolt] Intercepted link click:', url);

  try {
    chrome.runtime.sendMessage({
      type: 'link-download',
      url,
      referrer: location.href,
    });
  } catch {
    // Extension context invalidated (e.g. extension reloaded). Let the
    // browser handle the navigation normally by re-clicking the link.
    console.warn('[Bolt] Extension context invalidated — falling back to browser.');
    window.open(url, '_self');
  }
}, true); // Capture phase — runs before page handlers.
