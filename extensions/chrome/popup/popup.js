const statusDot = document.getElementById("status-dot");
const statusText = document.getElementById("status-text");
const captureToggle = document.getElementById("capture-toggle");
const minSize = document.getElementById("min-size");
const sizeUnit = document.getElementById("size-unit");
const extWhitelist = document.getElementById("ext-whitelist");
const extBlacklist = document.getElementById("ext-blacklist");
const domainBlocklist = document.getElementById("domain-blocklist");
const saveBtn = document.getElementById("save-filters");

const STATUS_LABELS = {
  ready: "Connected",
  daemon_unavailable: "Daemon not running",
  host_unavailable: "Host not installed",
};

// --- State ---

async function updateStatus() {
  const resp = await chrome.runtime.sendMessage({ type: "get-state" });
  const state = resp?.connectionState || "host_unavailable";
  statusDot.className = "status-dot " + state;
  statusText.textContent = STATUS_LABELS[state] || "Unknown";
}

// --- Settings ---

async function loadSettings() {
  const s = await chrome.storage.local.get({
    captureEnabled: false,
    minFileSize: 0,
    extensionWhitelist: [],
    extensionBlacklist: [],
    domainBlocklist: [],
  });

  captureToggle.checked = s.captureEnabled;

  // Convert bytes to display value
  if (s.minFileSize >= 1048576 && s.minFileSize % 1048576 === 0) {
    minSize.value = s.minFileSize / 1048576;
    sizeUnit.value = "1048576";
  } else if (s.minFileSize >= 1024) {
    minSize.value = Math.round(s.minFileSize / 1024);
    sizeUnit.value = "1024";
  } else {
    minSize.value = s.minFileSize;
    sizeUnit.value = "1024";
  }

  extWhitelist.value = s.extensionWhitelist.join(", ");
  extBlacklist.value = s.extensionBlacklist.join(", ");
  domainBlocklist.value = s.domainBlocklist.join(", ");
}

function parseList(value) {
  return value
    .split(",")
    .map((s) => s.trim().toLowerCase())
    .filter(Boolean);
}

function parseExtensionList(value) {
  return parseList(value).map((e) => (e.startsWith(".") ? e : "." + e));
}

// --- Event listeners ---

captureToggle.addEventListener("change", () => {
  chrome.storage.local.set({ captureEnabled: captureToggle.checked });
});

// Debug toggle
const debugToggle = document.getElementById("debug-toggle");

chrome.storage.local.get({ debugLogging: false }, (s) => {
  debugToggle.checked = s.debugLogging;
});

debugToggle.addEventListener("change", (e) => {
  chrome.storage.local.set({ debugLogging: e.target.checked });
});

saveBtn.addEventListener("click", () => {
  const sizeBytes = (parseInt(minSize.value, 10) || 0) * parseInt(sizeUnit.value, 10);
  chrome.storage.local.set({
    minFileSize: sizeBytes,
    extensionWhitelist: parseExtensionList(extWhitelist.value),
    extensionBlacklist: parseExtensionList(extBlacklist.value),
    domainBlocklist: parseList(domainBlocklist.value),
  });
  saveBtn.textContent = "Saved";
  setTimeout(() => (saveBtn.textContent = "Save"), 1500);
});

// --- Init ---
updateStatus();
loadSettings();
