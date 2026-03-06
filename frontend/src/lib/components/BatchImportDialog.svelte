<script lang="ts">
  import { getConfig } from "../state/config.svelte";
  import { loadDownloads } from "../state/downloads.svelte";

  interface Props {
    onClose: () => void;
  }

  let { onClose }: Props = $props();

  const app = (window as any).go.app.App;
  const cfg = $derived(getConfig());

  let urlText = $state("");
  let dir = $state("");
  let segments = $state(16);
  let submitting = $state(false);
  let results = $state<{ url: string; status: "pending" | "success" | "error"; error?: string }[]>([]);

  $effect(() => {
    if (cfg && !dir) {
      dir = cfg.download_dir;
      segments = cfg.default_segments;
    }
  });

  function parseUrls(text: string): string[] {
    return text
      .split("\n")
      .map((line) => line.trim())
      .filter((line) => /^https?:\/\//i.test(line));
  }

  const parsedUrls = $derived(parseUrls(urlText));

  async function importFromFile() {
    try {
      const path = await app.SelectTextFile();
      if (!path) return;
      const content = await app.ReadTextFile(path);
      if (content) {
        urlText = content;
      }
    } catch (e: any) {
      console.error("Import file failed:", e);
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
    const urls = parsedUrls;
    if (urls.length === 0) return;
    submitting = true;
    results = urls.map((url) => ({ url, status: "pending" as const }));

    for (let i = 0; i < urls.length; i++) {
      try {
        const result = await app.AddDownload({
          url: urls[i],
          filename: "",
          dir: dir,
          segments: segments,
          headers: {},
          referer_url: "",
          speed_limit: 0,
          checksum: null,
          force: true,
        });
        results[i] = { url: urls[i], status: "success" };
      } catch (e: any) {
        results[i] = { url: urls[i], status: "error", error: e?.message || String(e) };
      }
    }

    submitting = false;
    await loadDownloads();
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === "Escape") {
      onClose();
    }
  }

  const successCount = $derived(results.filter((r) => r.status === "success").length);
  const errorCount = $derived(results.filter((r) => r.status === "error").length);
  const processedCount = $derived(results.filter((r) => r.status !== "pending").length);
</script>

<svelte:window onkeydown={handleKeydown} />

<!-- Backdrop -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
  class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
  onmousedown={(e) => { if (e.target === e.currentTarget) onClose(); }}
>
  <div class="bg-white dark:bg-gray-800 rounded-lg shadow-xl w-[520px] max-h-[90vh] overflow-y-auto">
    <div class="px-6 py-4 border-b border-gray-200 dark:border-gray-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100">Batch Import</h2>
    </div>

    <div class="px-6 py-4 space-y-4">
      <!-- URL textarea -->
      <div>
        <div class="flex items-center justify-between mb-1">
          <label class="block text-sm font-medium text-gray-700 dark:text-gray-300" for="batch-urls">
            URLs (one per line)
          </label>
          <button
            onclick={importFromFile}
            class="text-xs text-blue-500 hover:text-blue-600 dark:text-blue-400"
          >
            Import from file
          </button>
        </div>
        <textarea
          id="batch-urls"
          bind:value={urlText}
          placeholder={"https://example.com/file1.zip\nhttps://example.com/file2.iso\nhttps://example.com/file3.tar.gz"}
          rows="6"
          class="w-full px-3 py-2 text-sm border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono resize-y"
          disabled={submitting}
        ></textarea>
        {#if parsedUrls.length > 0 && !submitting}
          <p class="text-xs text-gray-500 dark:text-gray-400 mt-1">
            {parsedUrls.length} valid URL{parsedUrls.length === 1 ? "" : "s"} found
          </p>
        {/if}
      </div>

      <!-- Directory -->
      <div>
        <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1" for="batch-dir">Save to</label>
        <div class="flex gap-2">
          <input
            id="batch-dir"
            type="text"
            bind:value={dir}
            class="flex-1 px-3 py-2 text-sm border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
            disabled={submitting}
          />
          <button
            onclick={selectDir}
            class="px-3 py-2 text-sm border border-gray-300 dark:border-gray-600 dark:text-gray-300 rounded-md hover:bg-gray-50 dark:hover:bg-gray-700"
            disabled={submitting}
          >
            Browse
          </button>
        </div>
      </div>

      <!-- Segments -->
      <div>
        <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1" for="batch-segments">
          Segments: {segments}
        </label>
        <input
          id="batch-segments"
          type="range"
          bind:value={segments}
          min="1"
          max="32"
          class="w-full"
          disabled={submitting}
        />
      </div>

      <!-- Progress / Results -->
      {#if results.length > 0}
        <div class="space-y-1">
          <div class="flex items-center justify-between text-sm text-gray-600 dark:text-gray-400">
            <span>Progress: {processedCount} / {results.length}</span>
            {#if processedCount === results.length}
              <span>
                {successCount} added{errorCount > 0 ? `, ${errorCount} failed` : ""}
              </span>
            {/if}
          </div>
          <!-- Progress bar -->
          <div class="h-1.5 bg-gray-200 dark:bg-gray-700 rounded-full overflow-hidden">
            <div
              class="h-full rounded-full transition-all duration-300 {errorCount > 0 ? 'bg-yellow-500' : 'bg-green-500'}"
              style="width: {results.length > 0 ? (processedCount / results.length) * 100 : 0}%"
            ></div>
          </div>
          <!-- Error list -->
          {#if errorCount > 0}
            <div class="mt-2 max-h-24 overflow-y-auto">
              {#each results.filter((r) => r.status === "error") as r}
                <div class="text-xs text-red-600 dark:text-red-400 truncate" title={r.error}>
                  {r.url}: {r.error}
                </div>
              {/each}
            </div>
          {/if}
        </div>
      {/if}
    </div>

    <div class="px-6 py-4 border-t border-gray-200 dark:border-gray-700 flex justify-end gap-3">
      <button
        onclick={onClose}
        class="px-4 py-2 text-sm text-gray-700 dark:text-gray-300 border border-gray-300 dark:border-gray-600 rounded-md hover:bg-gray-50 dark:hover:bg-gray-700"
      >
        {processedCount === results.length && results.length > 0 ? "Close" : "Cancel"}
      </button>
      {#if processedCount < results.length || results.length === 0}
        <button
          onclick={submit}
          disabled={parsedUrls.length === 0 || submitting}
          class="px-4 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {#if submitting}
            Adding {processedCount}/{results.length}...
          {:else}
            Import {parsedUrls.length} URL{parsedUrls.length === 1 ? "" : "s"}
          {/if}
        </button>
      {/if}
    </div>
  </div>
</div>
