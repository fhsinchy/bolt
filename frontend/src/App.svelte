<script lang="ts">
  import { onMount } from "svelte";
  import { initEventListeners, loadDownloads, getSelectedIds, clearSelection, selectAllDownloads, getDownloads } from "./lib/state/downloads.svelte";
  import { getConfig, loadConfig } from "./lib/state/config.svelte";
  import Toolbar from "./lib/components/Toolbar.svelte";
  import SearchBar from "./lib/components/SearchBar.svelte";
  import DownloadList from "./lib/components/DownloadList.svelte";
  import StatusBar from "./lib/components/StatusBar.svelte";
  import AddDownloadDialog from "./lib/components/AddDownloadDialog.svelte";
  import SettingsDialog from "./lib/components/SettingsDialog.svelte";
  import BatchImportDialog from "./lib/components/BatchImportDialog.svelte";
  import DownloadDetailsDialog from "./lib/components/DownloadDetailsDialog.svelte";
  import DuplicateDialog from "./lib/components/DuplicateDialog.svelte";

  let showAddDialog = $state(false);
  let showSettings = $state(false);
  let showBatchImport = $state(false);
  let showDetailsDialog = $state(false);
  let detailsDownloadId = $state("");
  let initialUrl = $state("");
  let duplicateInfo = $state<any>(null);

  const app = (window as any).go?.app?.App;

  async function handleKeydown(e: KeyboardEvent) {
    // Ctrl+Q always works, even when dialogs are open
    const isCtrlQ = (e.ctrlKey || e.metaKey) && e.key === 'q';

    // Skip shortcuts when dialogs are open (except Ctrl+Q)
    if ((showAddDialog || showSettings || showBatchImport || showDetailsDialog) && !isCtrlQ) return;

    // Skip when typing in form elements (except Ctrl+Q)
    const tag = (e.target as HTMLElement)?.tagName;
    if ((tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') && !isCtrlQ) return;

    if ((e.ctrlKey || e.metaKey) && e.key === 'n') {
      e.preventDefault();
      showAddDialog = true;
    } else if ((e.ctrlKey || e.metaKey) && e.key === 'v') {
      e.preventDefault();
      try {
        const text = await navigator.clipboard.readText();
        if (text && /^https?:\/\//i.test(text.trim())) {
          initialUrl = text.trim();
          showAddDialog = true;
        }
      } catch {
        // Clipboard access denied, ignore
      }
    } else if (e.key === 'Delete') {
      e.preventDefault();
      await deleteSelected();
    } else if (e.key === ' ') {
      e.preventDefault();
      await togglePauseSelected();
    } else if ((e.ctrlKey || e.metaKey) && e.key === 'a') {
      e.preventDefault();
      selectAllDownloads();
    } else if (isCtrlQ) {
      e.preventDefault();
      (window as any).runtime?.Quit();
    }
  }

  async function deleteSelected() {
    const ids = getSelectedIds();
    if (ids.size === 0) return;

    // Check if any selected downloads are active
    const downloads = getDownloads();
    const hasActive = downloads.some(d => ids.has(d.id) && d.status === 'active');

    if (hasActive) {
      if (!confirm('Some selected downloads are active. Remove them anyway?')) return;
    }

    for (const id of ids) {
      try {
        await app.CancelDownload(id, false);
      } catch (e) {
        console.error('Delete failed:', e);
      }
    }
    clearSelection();
  }

  async function togglePauseSelected() {
    const ids = getSelectedIds();
    if (ids.size === 0) return;

    const downloads = getDownloads();
    for (const id of ids) {
      const dl = downloads.find(d => d.id === id);
      if (!dl) continue;
      try {
        if (dl.status === 'active') {
          await app.PauseDownload(id);
        } else if (dl.status === 'paused') {
          await app.ResumeDownload(id);
        }
      } catch (e) {
        console.error('Toggle pause failed:', e);
      }
    }
  }

  function applyTheme(theme: string) {
    const doc = document.documentElement;
    if (theme === "dark") {
      doc.classList.add("dark");
    } else if (theme === "light") {
      doc.classList.remove("dark");
    } else {
      // system
      if (window.matchMedia("(prefers-color-scheme: dark)").matches) {
        doc.classList.add("dark");
      } else {
        doc.classList.remove("dark");
      }
    }
  }

  onMount(() => {
    initEventListeners();
    loadDownloads();
    loadConfig().then(() => {
      const cfg = getConfig();
      if (cfg) applyTheme(cfg.theme || "system");
    });

    // Listen for OS theme changes
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const handler = () => {
      const cfg = getConfig();
      if (!cfg || cfg.theme === "system") applyTheme("system");
    };
    mq.addEventListener("change", handler);

    const runtime = (window as any).runtime;
    if (runtime) {
      runtime.EventsOn("open_settings", () => {
        showSettings = true;
      });
      runtime.EventsOn("duplicate_detected", (data: any) => {
        duplicateInfo = data;
      });
    }

    return () => mq.removeEventListener("change", handler);
  });

  // Re-apply theme when config changes (e.g. after saving settings)
  $effect(() => {
    const cfg = getConfig();
    if (cfg) applyTheme(cfg.theme || "system");
  });
</script>

<svelte:window onkeydown={handleKeydown} />

<main class="flex flex-col h-screen bg-gray-50 dark:bg-gray-900 text-gray-900 dark:text-gray-100 select-none">
  <Toolbar
    onAdd={() => (showAddDialog = true)}
    onBatchImport={() => (showBatchImport = true)}
    onSettings={() => (showSettings = true)}
  />
  <SearchBar />
  <div class="flex-1 overflow-y-auto">
    <DownloadList onShowDetails={(id) => { detailsDownloadId = id; showDetailsDialog = true; }} />
  </div>
  <StatusBar />
</main>

{#if showAddDialog}
  <AddDownloadDialog initialUrl={initialUrl} onClose={() => { showAddDialog = false; initialUrl = ""; }} onDuplicate={(info) => { duplicateInfo = info; }} />
{/if}

{#if showSettings}
  <SettingsDialog onClose={() => (showSettings = false)} />
{/if}

{#if showBatchImport}
  <BatchImportDialog onClose={() => (showBatchImport = false)} />
{/if}

{#if showDetailsDialog}
  <DownloadDetailsDialog downloadId={detailsDownloadId} onClose={() => { showDetailsDialog = false; detailsDownloadId = ""; }} />
{/if}

{#if duplicateInfo}
  <DuplicateDialog info={duplicateInfo} onClose={() => { duplicateInfo = null; }} />
{/if}
