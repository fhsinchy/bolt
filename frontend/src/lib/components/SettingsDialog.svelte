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
      await saveConfig({
        download_dir: downloadDir,
        max_concurrent: maxConcurrent,
        default_segments: defaultSegments,
        max_retries: maxRetries,
        minimize_to_tray: minimizeToTray,
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
  <div class="bg-white rounded-lg shadow-xl w-[480px] max-h-[90vh] overflow-y-auto">
    <div class="px-6 py-4 border-b border-gray-200">
      <h2 class="text-lg font-semibold text-gray-900">Settings</h2>
    </div>

    <div class="px-6 py-4 space-y-4">
      <!-- Download directory -->
      <div>
        <label class="block text-sm font-medium text-gray-700 mb-1" for="settings-dir">Download Directory</label>
        <div class="flex gap-2">
          <input
            id="settings-dir"
            type="text"
            bind:value={downloadDir}
            class="flex-1 px-3 py-2 text-sm border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
          <button
            onclick={selectDir}
            class="px-3 py-2 text-sm border border-gray-300 rounded-md hover:bg-gray-50"
          >
            Browse
          </button>
        </div>
      </div>

      <!-- Max concurrent -->
      <div>
        <label class="block text-sm font-medium text-gray-700 mb-1" for="settings-concurrent">
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
        <label class="block text-sm font-medium text-gray-700 mb-1" for="settings-segments">
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
        <label class="block text-sm font-medium text-gray-700 mb-1" for="settings-retries">Max Retries</label>
        <input
          id="settings-retries"
          type="number"
          bind:value={maxRetries}
          min="0"
          max="100"
          class="w-24 px-3 py-2 text-sm border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
      </div>

      <!-- Minimize to tray -->
      <div class="flex items-center gap-2">
        <input
          id="settings-tray"
          type="checkbox"
          bind:checked={minimizeToTray}
          class="w-4 h-4 text-blue-500 border-gray-300 rounded focus:ring-blue-500"
        />
        <label class="text-sm font-medium text-gray-700" for="settings-tray">Minimize to tray on close</label>
      </div>

      <hr class="border-gray-200" />

      <!-- Server port (read-only) -->
      <div>
        <label class="block text-sm font-medium text-gray-700 mb-1" for="settings-port">Server Port</label>
        <input
          id="settings-port"
          type="text"
          value={serverPort}
          readonly
          class="w-24 px-3 py-2 text-sm bg-gray-50 border border-gray-200 rounded-md text-gray-500"
        />
      </div>

      <!-- Auth token (read-only with copy) -->
      <div>
        <label class="block text-sm font-medium text-gray-700 mb-1" for="settings-token">Auth Token</label>
        <div class="flex gap-2">
          <input
            id="settings-token"
            type="text"
            value={authToken}
            readonly
            class="flex-1 px-3 py-2 text-sm bg-gray-50 border border-gray-200 rounded-md text-gray-500 font-mono text-xs"
          />
          <button
            onclick={copyToken}
            class="px-3 py-2 text-sm border border-gray-300 rounded-md hover:bg-gray-50"
          >
            {copied ? "Copied!" : "Copy"}
          </button>
        </div>
      </div>

      {#if error}
        <div class="text-sm text-red-600 bg-red-50 px-3 py-2 rounded-md">
          {error}
        </div>
      {/if}
    </div>

    <div class="px-6 py-4 border-t border-gray-200 flex justify-end gap-3">
      <button
        onclick={onClose}
        class="px-4 py-2 text-sm text-gray-700 border border-gray-300 rounded-md hover:bg-gray-50"
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
