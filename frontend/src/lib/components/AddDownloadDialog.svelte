<script lang="ts">
  import { onMount } from "svelte";
  import type { ProbeResult } from "../types";
  import { formatBytes } from "../utils/format";
  import { getConfig } from "../state/config.svelte";

  interface Props {
    onClose: () => void;
    initialUrl?: string;
  }

  let { onClose, initialUrl = "" }: Props = $props();

  const app = (window as any).go.app.App;
  const cfg = $derived(getConfig());

  let url = $state(initialUrl);
  let filename = $state("");
  let dir = $state("");
  let segments = $state(16);
  let probing = $state(false);
  let probeResult = $state<ProbeResult | null>(null);
  let probeError = $state("");
  let submitting = $state(false);
  let submitError = $state("");
  let checksumOpen = $state(false);
  let checksumAlgo = $state("sha256");
  let checksumValue = $state("");

  // Initialize dir from config
  $effect(() => {
    if (cfg && !dir) {
      dir = cfg.download_dir;
      segments = cfg.default_segments;
    }
  });

  onMount(() => {
    if (initialUrl) {
      probe();
    }
  });

  async function probe() {
    if (!url.trim()) return;
    probing = true;
    probeError = "";
    probeResult = null;

    try {
      const result = await app.Probe(url.trim(), {});
      probeResult = result;
      if (result.filename && !filename) {
        filename = result.filename;
      }
    } catch (e: any) {
      probeError = e?.message || String(e);
    } finally {
      probing = false;
    }
  }

  function handleUrlKeydown(e: KeyboardEvent) {
    if (e.key === "Enter") {
      probe();
    }
  }

  async function selectDir() {
    try {
      const selected = await app.SelectDirectory();
      if (selected) {
        dir = selected;
      }
    } catch (e) {
      console.error("Select directory failed:", e);
    }
  }

  async function submit() {
    if (!url.trim()) return;
    submitting = true;
    submitError = "";

    try {
      await app.AddDownload({
        url: url.trim(),
        filename: filename,
        dir: dir,
        segments: segments,
        headers: {},
        referer_url: "",
        speed_limit: 0,
        checksum: checksumValue.trim()
          ? { algorithm: checksumAlgo, value: checksumValue.trim() }
          : null,
      });
      onClose();
    } catch (e: any) {
      submitError = e?.message || String(e);
    } finally {
      submitting = false;
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
      <h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100">Add Download</h2>
    </div>

    <div class="px-6 py-4 space-y-4">
      <!-- URL -->
      <div>
        <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1" for="url-input">URL</label>
        <input
          id="url-input"
          type="text"
          bind:value={url}
          onblur={probe}
          onkeydown={handleUrlKeydown}
          placeholder="https://example.com/file.zip"
          class="w-full px-3 py-2 text-sm border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
          autofocus
        />
      </div>

      <!-- Probe status -->
      {#if probing}
        <div class="flex items-center gap-2 text-sm text-gray-500">
          <div class="w-4 h-4 border-2 border-blue-500 border-t-transparent rounded-full animate-spin"></div>
          Checking URL...
        </div>
      {/if}

      {#if probeError}
        <div class="text-sm text-red-600 dark:text-red-400 bg-red-50 dark:bg-red-900/30 px-3 py-2 rounded-md">
          {probeError}
        </div>
      {/if}

      {#if probeResult}
        <div class="text-sm text-green-700 dark:text-green-300 bg-green-50 dark:bg-green-900/30 px-3 py-2 rounded-md">
          Ready — {probeResult.total_size > 0 ? formatBytes(probeResult.total_size) : "Unknown size"}
          {#if probeResult.accepts_ranges}
            — Resumable
          {/if}
        </div>
      {/if}

      <!-- Filename -->
      <div>
        <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1" for="filename-input">Filename</label>
        <input
          id="filename-input"
          type="text"
          bind:value={filename}
          placeholder="Auto-detected"
          class="w-full px-3 py-2 text-sm border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
      </div>

      <!-- Directory -->
      <div>
        <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1" for="dir-input">Save to</label>
        <div class="flex gap-2">
          <input
            id="dir-input"
            type="text"
            bind:value={dir}
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

      <!-- Segments -->
      <div>
        <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1" for="segments-input">
          Segments: {segments}
        </label>
        <input
          id="segments-input"
          type="range"
          bind:value={segments}
          min="1"
          max="32"
          class="w-full"
        />
      </div>

      <!-- Checksum (optional, collapsible) -->
      <div>
        <button
          type="button"
          onclick={() => (checksumOpen = !checksumOpen)}
          class="flex items-center gap-1 text-sm text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200"
        >
          <svg
            class="w-3.5 h-3.5 transition-transform {checksumOpen ? 'rotate-90' : ''}"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
          >
            <polyline points="9 18 15 12 9 6" />
          </svg>
          Checksum (optional)
        </button>

        {#if checksumOpen}
          <div class="mt-2 space-y-2">
            <div class="flex gap-2">
              <select
                bind:value={checksumAlgo}
                class="px-2 py-1.5 text-sm border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
              >
                <option value="md5">MD5</option>
                <option value="sha1">SHA-1</option>
                <option value="sha256">SHA-256</option>
                <option value="sha512">SHA-512</option>
              </select>
              <input
                type="text"
                bind:value={checksumValue}
                placeholder="Paste hash here"
                class="flex-1 px-3 py-1.5 text-sm font-mono border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
            <p class="text-xs text-gray-400 dark:text-gray-500">File will be verified after download completes.</p>
          </div>
        {/if}
      </div>

      {#if submitError}
        <div class="text-sm text-red-600 dark:text-red-400 bg-red-50 dark:bg-red-900/30 px-3 py-2 rounded-md">
          {submitError}
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
        onclick={submit}
        disabled={!url.trim() || submitting}
        class="px-4 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 disabled:opacity-50 disabled:cursor-not-allowed"
      >
        {submitting ? "Adding..." : "Download"}
      </button>
    </div>
  </div>
</div>
