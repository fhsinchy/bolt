const DEFAULT_CONFIG = {
  serverUrl: 'http://127.0.0.1:9683',
  authToken: '',
  captureEnabled: true,
};

const serverUrlInput = document.getElementById('server-url');
const authTokenInput = document.getElementById('auth-token');
const captureToggle = document.getElementById('capture-enabled');
const testBtn = document.getElementById('test-btn');
const saveBtn = document.getElementById('save-btn');
const toggleTokenBtn = document.getElementById('toggle-token');
const statusDot = document.getElementById('status-dot');
const statusText = document.getElementById('status-text');

// --- Load config on popup open ---

async function loadConfig() {
  const result = await chrome.storage.local.get('config');
  const config = { ...DEFAULT_CONFIG, ...result.config };

  serverUrlInput.value = config.serverUrl;
  authTokenInput.value = config.authToken;
  captureToggle.checked = config.captureEnabled;

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
  const config = {
    serverUrl: serverUrlInput.value.replace(/\/+$/, '') || DEFAULT_CONFIG.serverUrl,
    authToken: authTokenInput.value,
    captureEnabled: captureToggle.checked,
  };

  await chrome.storage.local.set({ config });

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
  const result = await chrome.storage.local.get('config');
  const config = { ...DEFAULT_CONFIG, ...result.config };
  config.captureEnabled = captureToggle.checked;
  await chrome.storage.local.set({ config });
});

toggleTokenBtn.addEventListener('click', () => {
  const isPassword = authTokenInput.type === 'password';
  authTokenInput.type = isPassword ? 'text' : 'password';
});

document.getElementById('chrome-downloads-link').addEventListener('click', (e) => {
  e.preventDefault();
  chrome.tabs.create({ url: 'chrome://settings/downloads' });
});

// --- Warning banner dismiss ---

const warningBanner = document.getElementById('save-dialog-warning');
const dismissBtn = document.getElementById('dismiss-warning');

async function loadWarningState() {
  const result = await chrome.storage.local.get('saveDialogWarningDismissed');
  if (result.saveDialogWarningDismissed) {
    warningBanner.classList.add('hidden');
  }
}

dismissBtn.addEventListener('click', async () => {
  warningBanner.classList.add('hidden');
  await chrome.storage.local.set({ saveDialogWarningDismissed: true });
});

// --- Init ---

loadConfig();
loadWarningState();
