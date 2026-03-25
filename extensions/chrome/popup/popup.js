const statusDot = document.getElementById("status-dot");
const statusText = document.getElementById("status-text");
const captureToggle = document.getElementById("capture-toggle");
const ignoreSiteBtn = document.getElementById("ignore-site");
const blocklistItemsEl = document.getElementById("blocklist-items");

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
  // Defaults are seeded by background.js onInstalled — fallbacks here are just safety
  const s = await chrome.storage.local.get({
    captureEnabled: false,
    domainBlocklist: [],
  });

  captureToggle.checked = s.captureEnabled;

  renderBlocklist(s.domainBlocklist);
  await updateIgnoreButton(s.domainBlocklist);
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

// --- Blocklist ---

let currentTabDomain = "";

async function getCurrentTabDomain() {
  try {
    const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
    if (tab?.url) return new URL(tab.url).hostname;
  } catch { /* ignore */ }
  return "";
}

function isDomainBlocked(domain, blocklist) {
  return blocklist.some((d) => {
    const blocked = d.trim();
    return domain === blocked || domain.endsWith("." + blocked);
  });
}

async function updateIgnoreButton(blocklist) {
  currentTabDomain = await getCurrentTabDomain();
  if (!currentTabDomain || currentTabDomain === "newtab" || !currentTabDomain.includes(".")) {
    ignoreSiteBtn.textContent = "Ignore this site";
    ignoreSiteBtn.disabled = true;
    return;
  }
  if (isDomainBlocked(currentTabDomain, blocklist)) {
    ignoreSiteBtn.textContent = "This site is ignored";
    ignoreSiteBtn.disabled = true;
  } else {
    ignoreSiteBtn.textContent = "Ignore this site";
    ignoreSiteBtn.disabled = false;
  }
}

function renderBlocklist(blocklist) {
  while (blocklistItemsEl.firstChild) {
    blocklistItemsEl.removeChild(blocklistItemsEl.firstChild);
  }
  for (const domain of blocklist) {
    const item = document.createElement("div");
    item.className = "blocklist-item";

    const label = document.createElement("span");
    label.textContent = domain;

    const removeBtn = document.createElement("button");
    removeBtn.textContent = "\u00d7";
    removeBtn.title = "Remove";
    removeBtn.addEventListener("click", () => removeDomain(domain));

    item.appendChild(label);
    item.appendChild(removeBtn);
    blocklistItemsEl.appendChild(item);
  }
}

async function addCurrentDomain() {
  if (!currentTabDomain) return;
  const s = await chrome.storage.local.get({ domainBlocklist: [] });
  if (isDomainBlocked(currentTabDomain, s.domainBlocklist)) return;
  s.domainBlocklist.push(currentTabDomain);
  await chrome.storage.local.set({ domainBlocklist: s.domainBlocklist });
  renderBlocklist(s.domainBlocklist);
  await updateIgnoreButton(s.domainBlocklist);
}

async function removeDomain(domain) {
  const s = await chrome.storage.local.get({ domainBlocklist: [] });
  s.domainBlocklist = s.domainBlocklist.filter((d) => d !== domain);
  await chrome.storage.local.set({ domainBlocklist: s.domainBlocklist });
  renderBlocklist(s.domainBlocklist);
  await updateIgnoreButton(s.domainBlocklist);
}

ignoreSiteBtn.addEventListener("click", addCurrentDomain);

// --- Init ---
updateStatus();
loadSettings();
