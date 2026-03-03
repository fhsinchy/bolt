<script lang="ts">
  import { onMount } from "svelte";
  import type { Download, DownloadDetail, Segment, ProbeResult } from "../types";
  import { formatBytes, formatSpeed, formatETA, formatDate } from "../utils/format";
  import { getDownloads } from "../state/downloads.svelte";
  import ProgressBar from "./ProgressBar.svelte";

  interface Props {
    downloadId: string;
    onClose: () => void;
  }

  let { downloadId, onClose }: Props = $props();

  const app = (window as any).go.app.App;

  // Download from reactive state (real-time speed/eta)
  const download = $derived(getDownloads().find((d) => d.id === downloadId));

  let segments = $state<Segment[]>([]);
  let pollTimer = $state<ReturnType<typeof setInterval> | null>(null);

  // Collapsible sections
  let segmentsOpen = $state(true);
  let urlOpen = $state(false);
  let checksumOpen = $state(false);
  let metadataOpen = $state(false);

  // URL editing
  let editingUrl = $state(false);
  let newUrl = $state("");
  let probing = $state(false);
  let probeResult = $state<ProbeResult | null>(null);
  let probeError = $state("");
  let refreshing = $state(false);
  let refreshError = $state("");

  // Checksum editing
  let editingChecksum = $state(false);
  let checksumAlgo = $state("sha256");
  let checksumValue = $state("");
  let savingChecksum = $state(false);
  let checksumError = $state("");

  // Initialize section defaults based on status
  $effect(() => {
    if (download) {
      if (download.status === "error" || download.status === "refresh") {
        urlOpen = true;
      }
    }
  });

  async function fetchDetail() {
    try {
      const detail: DownloadDetail = await app.GetDownloadDetail(downloadId);
      segments = detail.segments;
    } catch (e) {
      console.error("Failed to fetch detail:", e);
    }
  }

  onMount(() => {
    fetchDetail();
    pollTimer = setInterval(fetchDetail, 1000);
    return () => {
      if (pollTimer) clearInterval(pollTimer);
    };
  });

  // Stop polling when download is no longer active
  $effect(() => {
    if (download && download.status !== "active" && download.status !== "verifying") {
      if (pollTimer) {
        clearInterval(pollTimer);
        pollTimer = null;
      }
    } else if (download && (download.status === "active" || download.status === "verifying") && !pollTimer) {
      pollTimer = setInterval(fetchDetail, 1000);
    }
  });

  const isActive = $derived(download?.status === "active");
  const isCompleted = $derived(download?.status === "completed");
  const canEditChecksum = $derived(!isCompleted);

  const percentage = $derived.by(() => {
    if (!download || download.total_size <= 0) return 0;
    return Math.round((download.downloaded / download.total_size) * 100);
  });

  const segmentsDone = $derived(segments.filter((s) => s.done).length);

  // Action handlers (mirror ActionButtons logic)
  async function pause() {
    try { await app.PauseDownload(downloadId); } catch (e) { console.error("Pause failed:", e); }
  }
  async function resume() {
    try { await app.ResumeDownload(downloadId); } catch (e) { console.error("Resume failed:", e); }
  }
  async function retry() {
    try { await app.RetryDownload(downloadId); } catch (e) { console.error("Retry failed:", e); }
  }
  async function cancel() {
    if (!confirm("Cancel this download and delete the partial file?")) return;
    try {
      await app.CancelDownload(downloadId, true);
      onClose();
    } catch (e) { console.error("Cancel failed:", e); }
  }
  async function openFile() {
    if (!download) return;
    try { await app.OpenFile(download.dir + "/" + download.filename); } catch (e) { console.error("Open file failed:", e); }
  }
  async function openFolder() {
    if (!download) return;
    try { await app.OpenFolder(download.dir + "/" + download.filename); } catch (e) { console.error("Open folder failed:", e); }
  }

  // URL refresh
  async function probeNewUrl() {
    if (!newUrl.trim()) return;
    probing = true;
    probeError = "";
    probeResult = null;
    try {
      probeResult = await app.Probe(newUrl.trim(), {});
    } catch (e: any) {
      probeError = e?.message || String(e);
    } finally {
      probing = false;
    }
  }

  async function refreshUrl() {
    if (!newUrl.trim()) return;
    refreshing = true;
    refreshError = "";
    try {
      await app.RefreshURL(downloadId, newUrl.trim());
      editingUrl = false;
      newUrl = "";
      probeResult = null;
    } catch (e: any) {
      refreshError = e?.message || String(e);
    } finally {
      refreshing = false;
    }
  }

  // Checksum
  function startEditChecksum() {
    if (download?.checksum) {
      checksumAlgo = download.checksum.algorithm;
      checksumValue = download.checksum.value;
    } else {
      checksumAlgo = "sha256";
      checksumValue = "";
    }
    editingChecksum = true;
    checksumError = "";
  }

  async function saveChecksum() {
    savingChecksum = true;
    checksumError = "";
    try {
      await app.UpdateChecksum(downloadId, checksumAlgo, checksumValue.trim());
      editingChecksum = false;
    } catch (e: any) {
      checksumError = e?.message || String(e);
    } finally {
      savingChecksum = false;
    }
  }

  async function copyToClipboard(text: string) {
    try { await navigator.clipboard.writeText(text); } catch {}
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === "Escape") onClose();
  }

  function statusBadge(status: string) {
    switch (status) {
      case "completed": return { text: "Completed", class: "bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300" };
      case "active": return { text: "Downloading", class: "bg-blue-100 text-blue-700 dark:bg-blue-900 dark:text-blue-300" };
      case "queued": return { text: "Queued", class: "bg-yellow-100 text-yellow-700 dark:bg-yellow-900 dark:text-yellow-300" };
      case "error": return { text: "Error", class: "bg-red-100 text-red-700 dark:bg-red-900 dark:text-red-300" };
      case "paused": return { text: "Paused", class: "bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300" };
      case "refresh": return { text: "Refresh", class: "bg-orange-100 text-orange-700 dark:bg-orange-900 dark:text-orange-300" };
      case "verifying": return { text: "Verifying...", class: "bg-purple-100 text-purple-700 dark:bg-purple-900 dark:text-purple-300" };
      default: return { text: status, class: "bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300" };
    }
  }

  function formatByteRange(seg: Segment): string {
    if (seg.end_byte === 0 && seg.start_byte === 0) return "Unknown";
    return `${formatBytes(seg.start_byte)} - ${formatBytes(seg.end_byte)}`;
  }

  function segmentPercent(seg: Segment): number {
    const total = seg.end_byte - seg.start_byte + 1;
    if (total <= 0) return 0;
    return Math.round((seg.downloaded / total) * 100);
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
  <div class="bg-white dark:bg-gray-800 rounded-lg shadow-xl w-[640px] max-h-[90vh] overflow-y-auto">
    {#if !download}
      <div class="px-6 py-8 text-center text-gray-500 dark:text-gray-400">
        Download not found.
      </div>
    {:else}
      <!-- Header -->
      <div class="px-6 py-4 border-b border-gray-200 dark:border-gray-700">
        <div class="flex items-center gap-3">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100 truncate flex-1" title={download.filename}>
            {download.filename}
          </h2>
          <span class="text-xs px-2 py-0.5 rounded-full font-medium flex-shrink-0 {statusBadge(download.status).class}">
            {statusBadge(download.status).text}
          </span>
        </div>

        <!-- Progress -->
        <div class="mt-3">
          <ProgressBar
            downloaded={download.downloaded}
            totalSize={download.total_size}
            status={download.status}
          />
        </div>
        <div class="flex items-center gap-3 mt-1.5 text-sm text-gray-500 dark:text-gray-400">
          <span>
            {#if download.total_size > 0}
              {formatBytes(download.downloaded)} / {formatBytes(download.total_size)} ({percentage}%)
            {:else if download.downloaded > 0}
              {formatBytes(download.downloaded)}
            {:else}
              Unknown size
            {/if}
          </span>
          {#if isActive && download.speed}
            <span>{formatSpeed(download.speed)}</span>
          {/if}
          {#if isActive && download.eta}
            <span>ETA {formatETA(download.eta)}</span>
          {/if}
        </div>

        <!-- Action buttons -->
        <div class="flex items-center gap-2 mt-3">
          {#if download.status === "active"}
            <button onclick={pause} class="px-3 py-1.5 text-xs font-medium border border-gray-300 dark:border-gray-600 dark:text-gray-300 rounded-md hover:bg-gray-50 dark:hover:bg-gray-700">Pause</button>
          {/if}
          {#if download.status === "paused" || download.status === "queued"}
            <button onclick={resume} class="px-3 py-1.5 text-xs font-medium border border-gray-300 dark:border-gray-600 dark:text-gray-300 rounded-md hover:bg-gray-50 dark:hover:bg-gray-700">Resume</button>
          {/if}
          {#if download.status === "error"}
            <button onclick={retry} class="px-3 py-1.5 text-xs font-medium border border-gray-300 dark:border-gray-600 dark:text-gray-300 rounded-md hover:bg-gray-50 dark:hover:bg-gray-700">Retry</button>
          {/if}
          {#if isCompleted}
            <button onclick={openFile} class="px-3 py-1.5 text-xs font-medium border border-gray-300 dark:border-gray-600 dark:text-gray-300 rounded-md hover:bg-gray-50 dark:hover:bg-gray-700">Open File</button>
            <button onclick={openFolder} class="px-3 py-1.5 text-xs font-medium border border-gray-300 dark:border-gray-600 dark:text-gray-300 rounded-md hover:bg-gray-50 dark:hover:bg-gray-700">Open Folder</button>
          {/if}
          {#if !isCompleted}
            <button onclick={cancel} class="px-3 py-1.5 text-xs font-medium text-red-600 dark:text-red-400 border border-red-300 dark:border-red-700 rounded-md hover:bg-red-50 dark:hover:bg-red-900/30">Cancel</button>
          {/if}
        </div>
      </div>

      <div class="divide-y divide-gray-200 dark:divide-gray-700">
        <!-- Segments Section -->
        <div class="px-6 py-3">
          <button
            type="button"
            onclick={() => (segmentsOpen = !segmentsOpen)}
            class="flex items-center gap-1.5 w-full text-left text-sm font-medium text-gray-700 dark:text-gray-300"
          >
            <svg class="w-3.5 h-3.5 transition-transform {segmentsOpen ? 'rotate-90' : ''}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <polyline points="9 18 15 12 9 6" />
            </svg>
            Segments
            <span class="text-xs font-normal text-gray-400 dark:text-gray-500">
              ({segmentsDone}/{segments.length} complete)
            </span>
          </button>

          {#if segmentsOpen}
            <div class="mt-2 space-y-1.5 max-h-48 overflow-y-auto">
              {#each segments as seg (seg.index)}
                <div class="flex items-center gap-2 text-xs text-gray-600 dark:text-gray-400">
                  <span class="w-6 text-right font-mono text-gray-400 dark:text-gray-500">#{seg.index}</span>
                  <span class="w-28 text-right font-mono truncate" title={formatByteRange(seg)}>
                    {formatByteRange(seg)}
                  </span>
                  <div class="flex-1 bg-gray-200 dark:bg-gray-700 rounded-full h-1.5 overflow-hidden">
                    <div
                      class="h-full rounded-full transition-all {seg.done ? 'bg-green-500' : 'bg-blue-500'}"
                      style="width: {seg.done ? 100 : segmentPercent(seg)}%"
                    ></div>
                  </div>
                  <span class="w-10 text-right font-mono">
                    {#if seg.done}
                      Done
                    {:else}
                      {segmentPercent(seg)}%
                    {/if}
                  </span>
                </div>
              {/each}
            </div>
          {/if}
        </div>

        <!-- URL Section -->
        <div class="px-6 py-3">
          <button
            type="button"
            onclick={() => (urlOpen = !urlOpen)}
            class="flex items-center gap-1.5 w-full text-left text-sm font-medium text-gray-700 dark:text-gray-300"
          >
            <svg class="w-3.5 h-3.5 transition-transform {urlOpen ? 'rotate-90' : ''}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <polyline points="9 18 15 12 9 6" />
            </svg>
            URL
          </button>

          {#if urlOpen}
            <div class="mt-2 space-y-2">
              <div class="flex items-center gap-2">
                <code class="flex-1 text-xs font-mono text-gray-600 dark:text-gray-400 bg-gray-100 dark:bg-gray-700 px-2 py-1.5 rounded break-all">
                  {download.url}
                </code>
                <button
                  onclick={() => copyToClipboard(download.url)}
                  class="p-1 rounded hover:bg-gray-200 dark:hover:bg-gray-700 text-gray-500 dark:text-gray-400 flex-shrink-0"
                  title="Copy URL"
                >
                  <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
                    <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
                  </svg>
                </button>
              </div>

              {#if !isCompleted && !isActive}
                {#if !editingUrl}
                  <button
                    onclick={() => { editingUrl = true; newUrl = ""; probeResult = null; probeError = ""; refreshError = ""; }}
                    class="text-xs text-blue-600 dark:text-blue-400 hover:underline"
                  >
                    Edit URL...
                  </button>
                {:else}
                  <div class="space-y-2">
                    <input
                      type="text"
                      bind:value={newUrl}
                      placeholder="New URL"
                      class="w-full px-3 py-1.5 text-sm border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
                    />
                    <div class="flex items-center gap-2">
                      <button
                        onclick={probeNewUrl}
                        disabled={!newUrl.trim() || probing}
                        class="px-3 py-1.5 text-xs font-medium text-white bg-blue-500 rounded-md hover:bg-blue-600 disabled:opacity-50"
                      >
                        {probing ? "Probing..." : "Probe"}
                      </button>
                      {#if probeResult}
                        <button
                          onclick={refreshUrl}
                          disabled={refreshing}
                          class="px-3 py-1.5 text-xs font-medium text-white bg-green-500 rounded-md hover:bg-green-600 disabled:opacity-50"
                        >
                          {refreshing ? "Refreshing..." : "Refresh URL"}
                        </button>
                      {/if}
                      <button
                        onclick={() => { editingUrl = false; }}
                        class="px-3 py-1.5 text-xs font-medium text-gray-600 dark:text-gray-400 hover:text-gray-800 dark:hover:text-gray-200"
                      >
                        Cancel
                      </button>
                    </div>
                    {#if probeResult}
                      <div class="text-xs text-green-700 dark:text-green-300 bg-green-50 dark:bg-green-900/30 px-2 py-1.5 rounded">
                        Size: {probeResult.total_size > 0 ? formatBytes(probeResult.total_size) : "Unknown"}
                        {#if download.total_size > 0 && probeResult.total_size > 0}
                          {#if probeResult.total_size === download.total_size}
                            (matches)
                          {:else}
                            <span class="text-red-600 dark:text-red-400">(mismatch: original {formatBytes(download.total_size)})</span>
                          {/if}
                        {/if}
                      </div>
                    {/if}
                    {#if probeError}
                      <div class="text-xs text-red-600 dark:text-red-400 bg-red-50 dark:bg-red-900/30 px-2 py-1.5 rounded">{probeError}</div>
                    {/if}
                    {#if refreshError}
                      <div class="text-xs text-red-600 dark:text-red-400 bg-red-50 dark:bg-red-900/30 px-2 py-1.5 rounded">{refreshError}</div>
                    {/if}
                  </div>
                {/if}
              {/if}
            </div>
          {/if}
        </div>

        <!-- Checksum Section -->
        <div class="px-6 py-3">
          <button
            type="button"
            onclick={() => (checksumOpen = !checksumOpen)}
            class="flex items-center gap-1.5 w-full text-left text-sm font-medium text-gray-700 dark:text-gray-300"
          >
            <svg class="w-3.5 h-3.5 transition-transform {checksumOpen ? 'rotate-90' : ''}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <polyline points="9 18 15 12 9 6" />
            </svg>
            Checksum
            {#if download.checksum}
              <span class="text-xs font-normal text-gray-400 dark:text-gray-500 uppercase">{download.checksum.algorithm}</span>
            {/if}
          </button>

          {#if checksumOpen}
            <div class="mt-2 space-y-2">
              {#if download.checksum && !editingChecksum}
                <div class="text-xs">
                  <span class="font-medium text-gray-500 dark:text-gray-400 uppercase">{download.checksum.algorithm}:</span>
                  <code class="ml-1 font-mono text-gray-600 dark:text-gray-400 break-all">{download.checksum.value}</code>
                </div>
                {#if isCompleted}
                  <span class="inline-flex items-center gap-1 text-xs font-medium text-green-700 dark:text-green-300 bg-green-50 dark:bg-green-900/30 px-2 py-0.5 rounded">
                    <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="20 6 9 17 4 12" /></svg>
                    Verified
                  </span>
                {:else if download.status === "error" && download.error?.toLowerCase().includes("checksum")}
                  <span class="inline-flex items-center gap-1 text-xs font-medium text-red-700 dark:text-red-300 bg-red-50 dark:bg-red-900/30 px-2 py-0.5 rounded">
                    <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" /></svg>
                    Failed
                  </span>
                {/if}
                {#if canEditChecksum}
                  <button
                    onclick={startEditChecksum}
                    class="text-xs text-blue-600 dark:text-blue-400 hover:underline"
                  >
                    Edit...
                  </button>
                {/if}
              {:else if !editingChecksum}
                <p class="text-xs text-gray-400 dark:text-gray-500">No checksum set.</p>
                {#if canEditChecksum}
                  <button
                    onclick={startEditChecksum}
                    class="text-xs text-blue-600 dark:text-blue-400 hover:underline"
                  >
                    Add checksum...
                  </button>
                {/if}
              {/if}

              {#if editingChecksum}
                <div class="space-y-2">
                  <div class="flex gap-2">
                    <select
                      bind:value={checksumAlgo}
                      class="px-2 py-1.5 text-xs border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
                    >
                      <option value="md5">MD5</option>
                      <option value="sha1">SHA-1</option>
                      <option value="sha256">SHA-256</option>
                      <option value="sha512">SHA-512</option>
                    </select>
                    <input
                      type="text"
                      bind:value={checksumValue}
                      placeholder="Paste hash"
                      class="flex-1 px-2 py-1.5 text-xs font-mono border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
                    />
                  </div>
                  <div class="flex items-center gap-2">
                    <button
                      onclick={saveChecksum}
                      disabled={!checksumValue.trim() || savingChecksum}
                      class="px-3 py-1.5 text-xs font-medium text-white bg-blue-500 rounded-md hover:bg-blue-600 disabled:opacity-50"
                    >
                      {savingChecksum ? "Saving..." : "Save"}
                    </button>
                    <button
                      onclick={() => { editingChecksum = false; checksumError = ""; }}
                      class="px-3 py-1.5 text-xs font-medium text-gray-600 dark:text-gray-400 hover:text-gray-800 dark:hover:text-gray-200"
                    >
                      Cancel
                    </button>
                  </div>
                  {#if checksumError}
                    <div class="text-xs text-red-600 dark:text-red-400 bg-red-50 dark:bg-red-900/30 px-2 py-1.5 rounded">{checksumError}</div>
                  {/if}
                </div>
              {/if}
            </div>
          {/if}
        </div>

        <!-- Metadata Section -->
        <div class="px-6 py-3">
          <button
            type="button"
            onclick={() => (metadataOpen = !metadataOpen)}
            class="flex items-center gap-1.5 w-full text-left text-sm font-medium text-gray-700 dark:text-gray-300"
          >
            <svg class="w-3.5 h-3.5 transition-transform {metadataOpen ? 'rotate-90' : ''}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <polyline points="9 18 15 12 9 6" />
            </svg>
            Metadata
          </button>

          {#if metadataOpen && download}
            <div class="mt-2 space-y-1 text-xs">
              <div class="flex gap-2">
                <span class="w-24 flex-shrink-0 text-gray-400 dark:text-gray-500 text-right">ID</span>
                <span class="text-gray-700 dark:text-gray-300 font-mono break-all">{download.id}</span>
              </div>
              <div class="flex gap-2">
                <span class="w-24 flex-shrink-0 text-gray-400 dark:text-gray-500 text-right">Directory</span>
                <span class="text-gray-700 dark:text-gray-300 font-mono break-all">
                  {download.dir}
                  <button onclick={openFolder} class="ml-1 text-blue-600 dark:text-blue-400 hover:underline font-sans">Open</button>
                </span>
              </div>
              <div class="flex gap-2">
                <span class="w-24 flex-shrink-0 text-gray-400 dark:text-gray-500 text-right">Total Size</span>
                <span class="text-gray-700 dark:text-gray-300 font-mono break-all">{download.total_size > 0 ? formatBytes(download.total_size) : "Unknown"}</span>
              </div>
              <div class="flex gap-2">
                <span class="w-24 flex-shrink-0 text-gray-400 dark:text-gray-500 text-right">Downloaded</span>
                <span class="text-gray-700 dark:text-gray-300 font-mono break-all">{formatBytes(download.downloaded)}</span>
              </div>
              <div class="flex gap-2">
                <span class="w-24 flex-shrink-0 text-gray-400 dark:text-gray-500 text-right">Segments</span>
                <span class="text-gray-700 dark:text-gray-300 font-mono break-all">{download.segments}</span>
              </div>
              {#if download.etag}
                <div class="flex gap-2">
                  <span class="w-24 flex-shrink-0 text-gray-400 dark:text-gray-500 text-right">ETag</span>
                  <span class="text-gray-700 dark:text-gray-300 font-mono break-all">{download.etag}</span>
                </div>
              {/if}
              {#if download.last_modified}
                <div class="flex gap-2">
                  <span class="w-24 flex-shrink-0 text-gray-400 dark:text-gray-500 text-right">Last Modified</span>
                  <span class="text-gray-700 dark:text-gray-300 font-mono break-all">{download.last_modified}</span>
                </div>
              {/if}
              {#if download.referer_url}
                <div class="flex gap-2">
                  <span class="w-24 flex-shrink-0 text-gray-400 dark:text-gray-500 text-right">Referer</span>
                  <span class="text-gray-700 dark:text-gray-300 font-mono break-all">{download.referer_url}</span>
                </div>
              {/if}
              {#if download.headers && Object.keys(download.headers).length > 0}
                <div class="flex gap-2">
                  <span class="w-24 flex-shrink-0 text-gray-400 dark:text-gray-500 text-right">Headers</span>
                  <span class="text-gray-700 dark:text-gray-300 font-mono break-all">
                    {Object.entries(download.headers).map(([k, v]) => `${k}: ${v}`).join(", ")}
                  </span>
                </div>
              {/if}
              {#if download.error}
                <div class="flex gap-2">
                  <span class="w-24 flex-shrink-0 text-gray-400 dark:text-gray-500 text-right">Error</span>
                  <span class="text-red-600 dark:text-red-400 font-mono break-all">{download.error}</span>
                </div>
              {/if}
              <div class="flex gap-2">
                <span class="w-24 flex-shrink-0 text-gray-400 dark:text-gray-500 text-right">Created</span>
                <span class="text-gray-700 dark:text-gray-300 font-mono break-all">{download.created_at ? formatDate(download.created_at) : "-"}</span>
              </div>
              {#if download.completed_at}
                <div class="flex gap-2">
                  <span class="w-24 flex-shrink-0 text-gray-400 dark:text-gray-500 text-right">Completed</span>
                  <span class="text-gray-700 dark:text-gray-300 font-mono break-all">{formatDate(download.completed_at)}</span>
                </div>
              {/if}
            </div>
          {/if}
        </div>
      </div>

      <!-- Footer -->
      <div class="px-6 py-4 border-t border-gray-200 dark:border-gray-700 flex justify-end">
        <button
          onclick={onClose}
          class="px-4 py-2 text-sm text-gray-700 dark:text-gray-300 border border-gray-300 dark:border-gray-600 rounded-md hover:bg-gray-50 dark:hover:bg-gray-700"
        >
          Close
        </button>
      </div>
    {/if}
  </div>
</div>
