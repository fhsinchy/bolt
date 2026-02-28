<script lang="ts">
  import type { Download } from "../types";

  interface Props {
    download: Download;
  }

  let { download }: Props = $props();

  const app = (window as any).go.app.App;

  async function pause() {
    try {
      await app.PauseDownload(download.id);
    } catch (e) {
      console.error("Pause failed:", e);
    }
  }

  async function resume() {
    try {
      await app.ResumeDownload(download.id);
    } catch (e) {
      console.error("Resume failed:", e);
    }
  }

  async function retry() {
    try {
      await app.RetryDownload(download.id);
    } catch (e) {
      console.error("Retry failed:", e);
    }
  }

  async function cancel() {
    try {
      await app.CancelDownload(download.id, false);
    } catch (e) {
      console.error("Cancel failed:", e);
    }
  }

  async function openFile() {
    try {
      const path = download.dir + "/" + download.filename;
      await app.OpenFile(path);
    } catch (e) {
      console.error("Open file failed:", e);
    }
  }

  async function openFolder() {
    try {
      const path = download.dir + "/" + download.filename;
      await app.OpenFolder(path);
    } catch (e) {
      console.error("Open folder failed:", e);
    }
  }
</script>

<div class="flex items-center gap-1">
  {#if download.status === "active"}
    <button
      onclick={pause}
      class="p-1 rounded hover:bg-gray-200 text-gray-600"
      title="Pause"
    >
      <svg class="w-4 h-4" viewBox="0 0 24 24" fill="currentColor">
        <rect x="6" y="4" width="4" height="16" rx="1" />
        <rect x="14" y="4" width="4" height="16" rx="1" />
      </svg>
    </button>
  {/if}

  {#if download.status === "paused" || download.status === "queued"}
    <button
      onclick={resume}
      class="p-1 rounded hover:bg-gray-200 text-gray-600"
      title="Resume"
    >
      <svg class="w-4 h-4" viewBox="0 0 24 24" fill="currentColor">
        <path d="M8 5v14l11-7z" />
      </svg>
    </button>
  {/if}

  {#if download.status === "error"}
    <button
      onclick={retry}
      class="p-1 rounded hover:bg-gray-200 text-gray-600"
      title="Retry"
    >
      <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <path d="M1 4v6h6" />
        <path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10" />
      </svg>
    </button>
  {/if}

  {#if download.status === "completed"}
    <button
      onclick={openFile}
      class="p-1 rounded hover:bg-gray-200 text-gray-600"
      title="Open File"
    >
      <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
        <polyline points="15 3 21 3 21 9" />
        <line x1="10" y1="14" x2="21" y2="3" />
      </svg>
    </button>
    <button
      onclick={openFolder}
      class="p-1 rounded hover:bg-gray-200 text-gray-600"
      title="Open Folder"
    >
      <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
      </svg>
    </button>
  {/if}

  {#if download.status !== "completed"}
    <button
      onclick={cancel}
      class="p-1 rounded hover:bg-gray-200 text-red-500"
      title="Cancel"
    >
      <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <line x1="18" y1="6" x2="6" y2="18" />
        <line x1="6" y1="6" x2="18" y2="18" />
      </svg>
    </button>
  {/if}
</div>
