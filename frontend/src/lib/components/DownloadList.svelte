<script lang="ts">
  import { getFilteredDownloads } from "../state/downloads.svelte";
  import DownloadRow from "./DownloadRow.svelte";

  const downloads = $derived(getFilteredDownloads());
</script>

{#if downloads.length === 0}
  <div class="flex flex-col items-center justify-center h-full text-gray-400 dark:text-gray-500">
    <svg class="w-16 h-16 mb-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1">
      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
      <polyline points="7 10 12 15 17 10" />
      <line x1="12" y1="15" x2="12" y2="3" />
    </svg>
    <p class="text-lg font-medium">No downloads yet</p>
    <p class="text-sm mt-1">Click <strong>+</strong> to add one.</p>
  </div>
{:else}
  <div>
    {#each downloads as download (download.id)}
      <DownloadRow {download} />
    {/each}
  </div>
{/if}
