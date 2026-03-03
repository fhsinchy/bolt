<script lang="ts">
  import { getFilteredDownloads, getSearchQuery, getDownloads, reorderDownloads } from "../state/downloads.svelte";
  import DownloadRow from "./DownloadRow.svelte";

  interface Props {
    onShowDetails?: (id: string) => void;
  }

  let { onShowDetails }: Props = $props();

  const downloads = $derived(getFilteredDownloads());
  const isSearching = $derived(!!getSearchQuery());

  // Drag-and-drop state
  let draggedId = $state<string | null>(null);
  let dropTargetId = $state<string | null>(null);
  let dropPosition = $state<"above" | "below">("below");

  function handleDragStart(e: DragEvent, id: string) {
    if (isSearching) {
      e.preventDefault();
      return;
    }
    draggedId = id;
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = "move";
      e.dataTransfer.setData("text/plain", id);
    }
  }

  function handleDragOver(e: DragEvent, id: string) {
    if (!draggedId || draggedId === id || isSearching) return;
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = "move";

    // Determine above/below based on mouse Y position within the row
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const midY = rect.top + rect.height / 2;
    dropPosition = e.clientY < midY ? "above" : "below";
    dropTargetId = id;
  }

  function handleDragLeave(e: DragEvent, id: string) {
    // Only clear if actually leaving this element (not entering a child)
    const related = e.relatedTarget as HTMLElement | null;
    if (related && (e.currentTarget as HTMLElement).contains(related)) return;
    if (dropTargetId === id) {
      dropTargetId = null;
    }
  }

  async function handleDrop(e: DragEvent) {
    e.preventDefault();
    if (!draggedId || !dropTargetId || draggedId === dropTargetId || isSearching) {
      draggedId = null;
      dropTargetId = null;
      return;
    }

    // Use the full (unfiltered) downloads list for reordering
    const allDownloads = getDownloads();
    const ids = allDownloads.map((d) => d.id);
    const fromIdx = ids.indexOf(draggedId);
    const toIdx = ids.indexOf(dropTargetId);
    if (fromIdx === -1 || toIdx === -1) {
      draggedId = null;
      dropTargetId = null;
      return;
    }

    // Remove dragged item and reinsert at target position
    ids.splice(fromIdx, 1);
    const insertIdx = ids.indexOf(dropTargetId);
    if (dropPosition === "below") {
      ids.splice(insertIdx + 1, 0, draggedId);
    } else {
      ids.splice(insertIdx, 0, draggedId);
    }

    draggedId = null;
    dropTargetId = null;

    await reorderDownloads(ids);
  }

  function handleDragEnd() {
    draggedId = null;
    dropTargetId = null;
  }
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
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div ondrop={handleDrop} ondragend={handleDragEnd}>
    {#each downloads as download (download.id)}
      <DownloadRow
        {download}
        isDragging={draggedId === download.id}
        isDropTarget={dropTargetId === download.id}
        {dropPosition}
        draggable={!isSearching}
        onDragStart={(e) => handleDragStart(e, download.id)}
        onDragOver={(e) => handleDragOver(e, download.id)}
        onDragLeave={(e) => handleDragLeave(e, download.id)}
        onShowDetails={onShowDetails ? () => onShowDetails(download.id) : undefined}
      />
    {/each}
  </div>
{/if}
