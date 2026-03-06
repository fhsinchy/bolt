<script lang="ts">
  import type { Download } from "../types";
  import { formatBytes } from "../utils/format";

  interface DuplicateInfo {
    existing_id: string;
    filename: string;
    status: string;
    new_url: string;
    new_headers: Record<string, string> | null;
    existing?: Download;
  }

  interface Props {
    info: DuplicateInfo;
    onClose: () => void;
  }

  let { info, onClose }: Props = $props();

  const app = (window as any).go.app.App;

  let acting = $state(false);
  let error = $state("");

  async function refreshExisting() {
    acting = true;
    error = "";
    try {
      await app.RefreshURL(info.existing_id, info.new_url);
      await app.ResumeDownload(info.existing_id);
      onClose();
    } catch (e: any) {
      error = e?.message || String(e);
    } finally {
      acting = false;
    }
  }

  async function downloadAnyway() {
    acting = true;
    error = "";
    try {
      const req: any = {
        url: info.new_url,
        filename: "",
        dir: "",
        segments: 0,
        headers: info.new_headers || {},
        referer_url: "",
        speed_limit: 0,
        checksum: null,
        force: true,
      };
      await app.AddDownload(req);
      onClose();
    } catch (e: any) {
      error = e?.message || String(e);
    } finally {
      acting = false;
    }
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === "Escape") onClose();
  }

  function statusLabel(status: string): string {
    switch (status) {
      case "error": return "Failed";
      case "paused": return "Paused";
      case "queued": return "Queued";
      case "active": return "Downloading";
      case "refresh": return "Awaiting Refresh";
      default: return status;
    }
  }
</script>

<svelte:window onkeydown={handleKeydown} />

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
  class="fixed inset-0 bg-black/40 flex items-center justify-center z-[60]"
  onmousedown={(e) => { if (e.target === e.currentTarget) onClose(); }}
>
  <div class="bg-white dark:bg-gray-800 rounded-lg shadow-xl w-[440px]">
    <div class="px-6 py-4 border-b border-gray-200 dark:border-gray-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100">Duplicate Detected</h2>
    </div>

    <div class="px-6 py-4 space-y-3">
      <p class="text-sm text-gray-700 dark:text-gray-300">
        A download with filename <strong class="font-mono">{info.filename}</strong> already exists.
      </p>
      <div class="text-xs text-gray-500 dark:text-gray-400 bg-gray-50 dark:bg-gray-700/50 rounded-md px-3 py-2 space-y-1">
        <div>Status: <span class="font-medium">{statusLabel(info.status)}</span></div>
        {#if info.existing}
          <div>Progress: {formatBytes(info.existing.downloaded)} / {info.existing.total_size > 0 ? formatBytes(info.existing.total_size) : "Unknown"}</div>
        {/if}
      </div>

      {#if error}
        <div class="text-sm text-red-600 dark:text-red-400 bg-red-50 dark:bg-red-900/30 px-3 py-2 rounded-md">{error}</div>
      {/if}
    </div>

    <div class="px-6 py-4 border-t border-gray-200 dark:border-gray-700 flex justify-end gap-2">
      <button
        onclick={onClose}
        disabled={acting}
        class="px-4 py-2 text-sm text-gray-700 dark:text-gray-300 border border-gray-300 dark:border-gray-600 rounded-md hover:bg-gray-50 dark:hover:bg-gray-700 disabled:opacity-50"
      >
        Cancel
      </button>
      <button
        onclick={downloadAnyway}
        disabled={acting}
        class="px-4 py-2 text-sm text-gray-700 dark:text-gray-300 border border-gray-300 dark:border-gray-600 rounded-md hover:bg-gray-50 dark:hover:bg-gray-700 disabled:opacity-50"
      >
        {acting ? "..." : "Download Anyway"}
      </button>
      <button
        onclick={refreshExisting}
        disabled={acting}
        class="px-4 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 disabled:opacity-50"
      >
        {acting ? "..." : "Refresh Existing"}
      </button>
    </div>
  </div>
</div>
