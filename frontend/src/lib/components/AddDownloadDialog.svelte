<script lang="ts">
  import type { ProbeResult } from "../types";
  import { formatBytes } from "../utils/format";
  import { getConfig } from "../state/config.svelte";

  interface Props {
    onClose: () => void;
  }

  let { onClose }: Props = $props();

  const app = (window as any).go.app.App;
  const cfg = $derived(getConfig());

  let url = $state("");
  let filename = $state("");
  let dir = $state("");
  let segments = $state(16);
  let probing = $state(false);
  let probeResult = $state<ProbeResult | null>(null);
  let probeError = $state("");
  let submitting = $state(false);
  let submitError = $state("");

  // Initialize dir from config
  $effect(() => {
    if (cfg && !dir) {
      dir = cfg.download_dir;
      segments = cfg.default_segments;
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
        checksum: null,
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
  <div class="bg-white rounded-lg shadow-xl w-[480px] max-h-[90vh] overflow-y-auto">
    <div class="px-6 py-4 border-b border-gray-200">
      <h2 class="text-lg font-semibold text-gray-900">Add Download</h2>
    </div>

    <div class="px-6 py-4 space-y-4">
      <!-- URL -->
      <div>
        <label class="block text-sm font-medium text-gray-700 mb-1" for="url-input">URL</label>
        <input
          id="url-input"
          type="text"
          bind:value={url}
          onblur={probe}
          onkeydown={handleUrlKeydown}
          placeholder="https://example.com/file.zip"
          class="w-full px-3 py-2 text-sm border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
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
        <div class="text-sm text-red-600 bg-red-50 px-3 py-2 rounded-md">
          {probeError}
        </div>
      {/if}

      {#if probeResult}
        <div class="text-sm text-green-700 bg-green-50 px-3 py-2 rounded-md">
          Ready — {probeResult.total_size > 0 ? formatBytes(probeResult.total_size) : "Unknown size"}
          {#if probeResult.accepts_ranges}
            — Resumable
          {/if}
        </div>
      {/if}

      <!-- Filename -->
      <div>
        <label class="block text-sm font-medium text-gray-700 mb-1" for="filename-input">Filename</label>
        <input
          id="filename-input"
          type="text"
          bind:value={filename}
          placeholder="Auto-detected"
          class="w-full px-3 py-2 text-sm border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
      </div>

      <!-- Directory -->
      <div>
        <label class="block text-sm font-medium text-gray-700 mb-1" for="dir-input">Save to</label>
        <div class="flex gap-2">
          <input
            id="dir-input"
            type="text"
            bind:value={dir}
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

      <!-- Segments -->
      <div>
        <label class="block text-sm font-medium text-gray-700 mb-1" for="segments-input">
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

      {#if submitError}
        <div class="text-sm text-red-600 bg-red-50 px-3 py-2 rounded-md">
          {submitError}
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
        onclick={submit}
        disabled={!url.trim() || submitting}
        class="px-4 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 disabled:opacity-50 disabled:cursor-not-allowed"
      >
        {submitting ? "Adding..." : "Download"}
      </button>
    </div>
  </div>
</div>
