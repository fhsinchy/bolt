<script lang="ts">
  interface Props {
    downloaded: number;
    totalSize: number;
    status: string;
  }

  let { downloaded, totalSize, status }: Props = $props();

  const percentage = $derived(
    totalSize > 0 ? Math.min(100, Math.round((downloaded / totalSize) * 100)) : 0,
  );

  const indeterminate = $derived(totalSize <= 0 && status === "active");

  const barColor = $derived.by(() => {
    switch (status) {
      case "completed":
        return "bg-green-500";
      case "error":
        return "bg-red-500";
      case "paused":
        return "bg-gray-400";
      default:
        return "bg-blue-500";
    }
  });
</script>

<div class="flex items-center gap-2 w-full">
  <div class="flex-1 h-2 bg-gray-200 rounded-full overflow-hidden">
    {#if indeterminate}
      <div class="h-full bg-blue-500 rounded-full animate-pulse w-full"></div>
    {:else}
      <div
        class="h-full {barColor} rounded-full transition-all duration-300"
        style="width: {percentage}%"
      ></div>
    {/if}
  </div>
  <span class="text-xs text-gray-500 w-10 text-right tabular-nums">
    {#if indeterminate}
      --
    {:else}
      {percentage}%
    {/if}
  </span>
</div>
