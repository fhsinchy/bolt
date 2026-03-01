<script lang="ts">
  import { getDownloads, getActiveDownloads, getTotalSpeed } from "../state/downloads.svelte";
  import { formatSpeed } from "../utils/format";

  const downloads = $derived(getDownloads());
  const activeDownloads = $derived(getActiveDownloads());
  const totalSpeed = $derived(getTotalSpeed());

  const queuedCount = $derived(
    downloads.filter((d) => d.status === "queued").length,
  );
</script>

<div
  class="flex items-center gap-4 px-4 py-1.5 bg-white dark:bg-gray-800 border-t border-gray-200 dark:border-gray-700 text-xs text-gray-500 dark:text-gray-400"
>
  <span>{activeDownloads.length} active</span>
  <span>{queuedCount} queued</span>
  {#if totalSpeed > 0}
    <span>{formatSpeed(totalSpeed)}</span>
  {/if}
  <div class="flex-1"></div>
  <span>{downloads.length} total</span>
</div>
