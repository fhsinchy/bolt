const DEFAULT_CONFIG = {
  serverUrl: 'http://127.0.0.1:9683',
  authToken: '',
  captureEnabled: true,
  minFileSize: 0,
  extensionWhitelist: [],
  extensionBlacklist: [],
  domainBlocklist: [],
};

const serverUrlInput = document.getElementById('server-url');
const authTokenInput = document.getElementById('auth-token');
const captureToggle = document.getElementById('capture-enabled');
const testBtn = document.getElementById('test-btn');
const saveBtn = document.getElementById('save-btn');
const toggleTokenBtn = document.getElementById('toggle-token');
const statusDot = document.getElementById('status-dot');
const statusText = document.getElementById('status-text');
const filterToggleBtn = document.getElementById('filter-toggle');
const filterArrow = document.getElementById('filter-arrow');
const filterContent = document.getElementById('filter-content');
const minFileSizeInput = document.getElementById('min-file-size');
const minFileSizeUnit = document.getElementById('min-file-size-unit');
const extWhitelist = document.getElementById('ext-whitelist');
const extBlacklist = document.getElementById('ext-blacklist');
const domainBlocklist = document.getElementById('domain-blocklist');

// --- Filter helpers ---

function normalizeExtension(ext) {
  ext = ext.trim().toLowerCase();
  if (ext && !ext.startsWith('.')) ext = '.' + ext;
  return ext;
}

function parseList(text) {
  return text.split(',').map(s => s.trim()).filter(Boolean);
}

// --- Load config on popup open ---

async function loadConfig() {
  const result = await browser.storage.local.get('config');
  const config = { ...DEFAULT_CONFIG, ...result.config };

  serverUrlInput.value = config.serverUrl;
  authTokenInput.value = config.authToken;
  captureToggle.checked = config.captureEnabled;

  // Populate filter fields
  const sizeBytes = config.minFileSize || 0;
  if (sizeBytes >= 1024 * 1024 && sizeBytes % (1024 * 1024) === 0) {
    minFileSizeInput.value = sizeBytes / (1024 * 1024);
    minFileSizeUnit.value = 'MB';
  } else {
    minFileSizeInput.value = Math.round(sizeBytes / 1024);
    minFileSizeUnit.value = 'KB';
  }
  extWhitelist.value = (config.extensionWhitelist || []).join(', ');
  extBlacklist.value = (config.extensionBlacklist || []).join(', ');
  domainBlocklist.value = (config.domainBlocklist || []).join(', ');

  testConnection(config.serverUrl, config.authToken);
}

// --- Connection test ---

async function testConnection(serverUrl, token) {
  statusDot.className = 'status-dot';
  statusText.textContent = 'Checking...';
  testBtn.disabled = true;

  // Dev: right-click popup → Inspect to see console output
  console.log('[Bolt] Testing connection to', serverUrl);

  try {
    const headers = {};
    if (token) headers['Authorization'] = `Bearer ${token}`;

    const resp = await fetch(`${serverUrl}/api/stats`, {
      method: 'GET',
      headers,
      signal: AbortSignal.timeout(3000),
    });

    console.log('[Bolt] Response:', resp.status, resp.statusText);

    if (resp.ok) {
      const data = await resp.json();
      statusDot.classList.add('connected');
      statusText.textContent = `Connected (v${data.version || '?'})`;
    } else if (resp.status === 401) {
      statusDot.classList.add('disconnected');
      statusText.textContent = token
        ? 'Token rejected — check config.json'
        : 'Auth token required';
    } else {
      // 404 likely means another service (e.g. aria2) is on this port, not Bolt
      statusDot.classList.add('disconnected');
      statusText.textContent = resp.status === 404
        ? 'Not Bolt — wrong port? (got 404)'
        : `Unexpected response (${resp.status})`;
    }
  } catch (err) {
    console.warn('[Bolt] Connection failed:', err.message);
    statusDot.classList.add('disconnected');
    statusText.textContent = 'Not reachable — is Bolt running?';
  } finally {
    testBtn.disabled = false;
  }
}

// --- Save config ---

async function saveConfig() {
  const sizeVal = parseFloat(minFileSizeInput.value) || 0;
  const sizeUnit = minFileSizeUnit.value;
  const sizeBytes = sizeUnit === 'MB' ? sizeVal * 1024 * 1024 : sizeVal * 1024;

  const config = {
    serverUrl: serverUrlInput.value.replace(/\/+$/, '') || DEFAULT_CONFIG.serverUrl,
    authToken: authTokenInput.value,
    captureEnabled: captureToggle.checked,
    minFileSize: sizeBytes,
    extensionWhitelist: parseList(extWhitelist.value).map(normalizeExtension).filter(Boolean),
    extensionBlacklist: parseList(extBlacklist.value).map(normalizeExtension).filter(Boolean),
    domainBlocklist: parseList(domainBlocklist.value).map(s => s.trim().toLowerCase()).filter(Boolean),
  };

  await browser.storage.local.set({ config });

  saveBtn.textContent = 'Saved';
  setTimeout(() => {
    saveBtn.textContent = 'Save';
  }, 1500);
}

// --- Event listeners ---

testBtn.addEventListener('click', () => {
  testConnection(serverUrlInput.value.replace(/\/+$/, ''), authTokenInput.value);
});

saveBtn.addEventListener('click', saveConfig);

captureToggle.addEventListener('change', async () => {
  const result = await browser.storage.local.get('config');
  const config = { ...DEFAULT_CONFIG, ...result.config };
  config.captureEnabled = captureToggle.checked;
  await browser.storage.local.set({ config });
});

toggleTokenBtn.addEventListener('click', () => {
  const isPassword = authTokenInput.type === 'password';
  authTokenInput.type = isPassword ? 'text' : 'password';
});

filterToggleBtn.addEventListener('click', () => {
  filterContent.classList.toggle('hidden');
  filterArrow.classList.toggle('open');
});

// --- Init ---

loadConfig();
