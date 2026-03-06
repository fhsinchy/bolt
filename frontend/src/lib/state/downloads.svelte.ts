import type { Download, ProgressUpdate } from "../types";

// Reactive state
let downloads = $state<Download[]>([]);
let searchQuery = $state("");
let selectedIds = $state<Set<string>>(new Set());

// Derived
const filteredDownloads = $derived.by(() => {
  if (!searchQuery) return downloads;
  const q = searchQuery.toLowerCase();
  return downloads.filter(
    (d) =>
      d.filename.toLowerCase().includes(q) ||
      d.url.toLowerCase().includes(q),
  );
});

const activeDownloads = $derived(
  downloads.filter((d) => d.status === "active"),
);

const totalSpeed = $derived(
  activeDownloads.reduce((sum, d) => sum + (d.speed ?? 0), 0),
);

export function getDownloads() {
  return downloads;
}

export function getFilteredDownloads() {
  return filteredDownloads;
}

export function getActiveDownloads() {
  return activeDownloads;
}

export function getTotalSpeed() {
  return totalSpeed;
}

export function getSearchQuery() {
  return searchQuery;
}

export function setSearchQuery(q: string) {
  searchQuery = q;
}

export function getSelectedIds() {
  return selectedIds;
}

export function toggleSelected(id: string) {
  if (selectedIds.has(id)) {
    selectedIds.delete(id);
  } else {
    selectedIds.add(id);
  }
  selectedIds = new Set(selectedIds);
}

export function clearSelection() {
  selectedIds = new Set();
}

export function selectAllDownloads() {
  selectedIds = new Set(downloads.map(d => d.id));
}

export async function reorderDownloads(orderedIds: string[]) {
  // Optimistic: reorder local array to match
  const idOrder = new Map(orderedIds.map((id, i) => [id, i]));
  downloads = [...downloads].sort((a, b) => {
    const ai = idOrder.get(a.id) ?? Infinity;
    const bi = idOrder.get(b.id) ?? Infinity;
    return ai - bi;
  });

  try {
    await (window as any).go.app.App.ReorderDownloads(orderedIds);
  } catch (e) {
    console.error("Reorder failed:", e);
    await loadDownloads();
  }
}

export async function loadDownloads() {
  try {
    const result = await (window as any).go.app.App.ListDownloads("", 0, 0);
    downloads = result ?? [];
  } catch (e) {
    console.error("Failed to load downloads:", e);
  }
}

function updateDownloadProgress(update: ProgressUpdate) {
  const idx = downloads.findIndex((d) => d.id === update.id);
  if (idx >= 0) {
    downloads[idx] = {
      ...downloads[idx],
      downloaded: update.downloaded,
      total_size: update.total_size || downloads[idx].total_size,
      speed: update.speed,
      eta: update.eta,
      status: update.status || downloads[idx].status,
    };
  }
}

function updateDownloadStatus(id: string, status: Download["status"]) {
  const idx = downloads.findIndex((d) => d.id === id);
  if (idx >= 0) {
    downloads[idx] = {
      ...downloads[idx],
      status,
      speed: status === "active" ? downloads[idx].speed : 0,
      eta: status === "active" ? downloads[idx].eta : 0,
    };
  }
}

export function initEventListeners() {
  const runtime = (window as any).runtime;
  if (!runtime) return;

  runtime.EventsOn("progress", (data: ProgressUpdate) => {
    updateDownloadProgress(data);
  });

  runtime.EventsOn("download_added", () => {
    loadDownloads();
  });

  runtime.EventsOn("download_completed", (data: { id: string }) => {
    updateDownloadStatus(data.id, "completed");
  });

  runtime.EventsOn("download_failed", (data: { id: string }) => {
    updateDownloadStatus(data.id, "error");
  });

  runtime.EventsOn("download_paused", (data: { id: string }) => {
    updateDownloadStatus(data.id, "paused");
  });

  runtime.EventsOn("download_resumed", (data: { id: string }) => {
    updateDownloadStatus(data.id, "queued");
  });

  runtime.EventsOn("download_removed", (data: { id: string }) => {
    downloads = downloads.filter((d) => d.id !== data.id);
    selectedIds.delete(data.id);
    selectedIds = new Set(selectedIds);
  });
}
