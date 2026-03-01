<script lang="ts">
  import { getConfig, loadConfig, saveConfig } from "../state/config.svelte";
  import { onMount } from "svelte";

  interface Props {
    onClose: () => void;
  }

  let { onClose }: Props = $props();

  const app = (window as any).go.app.App;

  let downloadDir = $state("");
  let maxConcurrent = $state(3);
  let defaultSegments = $state(16);
  let maxRetries = $state(10);
  let minimizeToTray = $state(true);
  let speedLimitValue = $state(0);
  let speedLimitUnit = $state("MB");
  let theme = $state("system");
  let serverPort = $state(9683);
  let authToken = $state("");
  let saving = $state(false);
  let error = $state("");
  let copied = $state(false);

  onMount(async () => {
    await loadConfig();
    const cfg = getConfig();
    if (cfg) {
      downloadDir = cfg.download_dir;
      maxConcurrent = cfg.max_concurrent;
      defaultSegments = cfg.default_segments;
      maxRetries = cfg.max_retries;
      minimizeToTray = cfg.minimize_to_tray;
      serverPort = cfg.server_port;
      theme = cfg.theme || "system";

      // Convert bytes/sec to display value + unit
      const bytesPerSec = cfg.global_speed_limit || 0;
      if (bytesPerSec >= 1048576 && bytesPerSec % 1048576 === 0) {
        speedLimitValue = bytesPerSec / 1048576;
        speedLimitUnit = "MB";
      } else if (bytesPerSec > 0) {
        speedLimitValue = Math.round(bytesPerSec / 1024);
        speedLimitUnit = "KB";
      } else {
        speedLimitValue = 0;
        speedLimitUnit = "MB";
      }
    }
    try {
      authToken = await app.GetAuthToken();
    } catch {
      authToken = "(unavailable)";
    }
  });

  async function selectDir() {
    try {
      const selected = await app.SelectDirectory();
      if (selected) {
        downloadDir = selected;
      }
    } catch (e) {
      console.error("Select directory failed:", e);
    }
  }

  async function save() {
    saving = true;
    error = "";
    try {
      // Convert display value + unit back to bytes/sec
      let globalSpeedLimit = 0;
      if (speedLimitValue > 0) {
        globalSpeedLimit = speedLimitUnit === "MB"
          ? speedLimitValue * 1048576
          : speedLimitValue * 1024;
      }

      await saveConfig({
        download_dir: downloadDir,
        max_concurrent: maxConcurrent,
        default_segments: defaultSegments,
        max_retries: maxRetries,
        minimize_to_tray: minimizeToTray,
        global_speed_limit: globalSpeedLimit,
        theme: theme,
      });
      onClose();
    } catch (e: any) {
      error = e?.message || String(e);
    } finally {
      saving = false;
    }
  }

  async function copyToken() {
    try {
      await navigator.clipboard.writeText(authToken);
      copied = true;
      setTimeout(() => (copied = false), 2000);
    } catch {
      // Fallback: select and let user copy
    }
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === "Escape") {
      onClose();
    }
  }
</script>

<svelte:window onkeydown={handleKeydown} />

<!-- Backdrop -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
  class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
  onmousedown={(e) => { if (e.target === e.currentTarget) onClose(); }}
>
  <!-- Dialog -->
  <div class="bg-white dark:bg-gray-800 rounded-lg shadow-xl w-[480px] max-h-[90vh] overflow-y-auto">
    <div class="px-6 py-4 border-b border-gray-200 dark:border-gray-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100">Settings</h2>
    </div>

    <div class="px-6 py-4 space-y-4">
      <!-- Download directory -->
      <div>
        <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1" for="settings-dir">Download Directory</label>
        <div class="flex gap-2">
          <input
            id="settings-dir"
            type="text"
            bind:value={downloadDir}
            class="flex-1 px-3 py-2 text-sm border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
          <button
            onclick={selectDir}
            class="px-3 py-2 text-sm border border-gray-300 dark:border-gray-600 dark:text-gray-300 rounded-md hover:bg-gray-50 dark:hover:bg-gray-700"
          >
            Browse
          </button>
        </div>
      </div>

      <!-- Max concurrent -->
      <div>
        <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1" for="settings-concurrent">
          Max Concurrent Downloads: {maxConcurrent}
        </label>
        <input
          id="settings-concurrent"
          type="range"
          bind:value={maxConcurrent}
          min="1"
          max="10"
          class="w-full"
        />
      </div>

      <!-- Default segments -->
      <div>
        <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1" for="settings-segments">
          Default Segments: {defaultSegments}
        </label>
        <input
          id="settings-segments"
          type="range"
          bind:value={defaultSegments}
          min="1"
          max="32"
          class="w-full"
        />
      </div>

      <!-- Max retries -->
      <div>
        <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1" for="settings-retries">Max Retries</label>
        <input
          id="settings-retries"
          type="number"
          bind:value={maxRetries}
          min="0"
          max="100"
          class="w-24 px-3 py-2 text-sm border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
      </div>

      <!-- Minimize to tray -->
      <div>
        <div class="flex items-center gap-2">
          <input
            id="settings-tray"
            type="checkbox"
            bind:checked={minimizeToTray}
            class="w-4 h-4 text-blue-500 border-gray-300 rounded focus:ring-blue-500"
          />
          <label class="text-sm font-medium text-gray-700 dark:text-gray-300" for="settings-tray">Minimize to tray on close</label>
        </div>
        {#if !minimizeToTray}
          <p class="mt-1 ml-6 text-xs text-amber-600">Closing the window will quit the application.</p>
        {/if}
      </div>

      <!-- Speed limit -->
      <div>
        <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1" for="settings-speed">Speed Limit</label>
        <div class="flex items-center gap-2">
          <input
            id="settings-speed"
            type="number"
            bind:value={speedLimitValue}
            min="0"
            class="w-24 px-3 py-2 text-sm border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
          <select
            bind:value={speedLimitUnit}
            class="px-3 py-2 text-sm border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
          >
            <option value="KB">KB/s</option>
            <option value="MB">MB/s</option>
          </select>
          <span class="text-xs text-gray-400 dark:text-gray-500">0 = unlimited</span>
        </div>
      </div>

      <!-- Theme -->
      <div>
        <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1" for="settings-theme">Theme</label>
        <select
          id="settings-theme"
          bind:value={theme}
          class="px-3 py-2 text-sm border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
        >
          <option value="system">System</option>
          <option value="light">Light</option>
          <option value="dark">Dark</option>
        </select>
      </div>

      <hr class="border-gray-200 dark:border-gray-700" />

      <!-- Server port (read-only) -->
      <div>
        <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1" for="settings-port">Server Port</label>
        <input
          id="settings-port"
          type="text"
          value={serverPort}
          readonly
          class="w-24 px-3 py-2 text-sm bg-gray-50 dark:bg-gray-700 border border-gray-200 dark:border-gray-600 rounded-md text-gray-500 dark:text-gray-400"
        />
      </div>

      <!-- Auth token (read-only with copy) -->
      <div>
        <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1" for="settings-token">Auth Token</label>
        <div class="flex gap-2">
          <input
            id="settings-token"
            type="text"
            value={authToken}
            readonly
            class="flex-1 px-3 py-2 text-sm bg-gray-50 dark:bg-gray-700 border border-gray-200 dark:border-gray-600 rounded-md text-gray-500 dark:text-gray-400 font-mono text-xs"
          />
          <button
            onclick={copyToken}
            class="px-3 py-2 text-sm border border-gray-300 dark:border-gray-600 dark:text-gray-300 rounded-md hover:bg-gray-50 dark:hover:bg-gray-700"
          >
            {copied ? "Copied!" : "Copy"}
          </button>
        </div>
      </div>

      {#if error}
        <div class="text-sm text-red-600 dark:text-red-400 bg-red-50 dark:bg-red-900/30 px-3 py-2 rounded-md">
          {error}
        </div>
      {/if}
    </div>

    <div class="px-6 py-4 border-t border-gray-200 dark:border-gray-700 flex justify-end gap-3">
      <button
        onclick={onClose}
        class="px-4 py-2 text-sm text-gray-700 dark:text-gray-300 border border-gray-300 dark:border-gray-600 rounded-md hover:bg-gray-50 dark:hover:bg-gray-700"
      >
        Cancel
      </button>
      <button
        onclick={save}
        disabled={saving}
        class="px-4 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 disabled:opacity-50"
      >
        {saving ? "Saving..." : "Save"}
      </button>
    </div>
  </div>
</div>
