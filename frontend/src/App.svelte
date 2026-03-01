<script lang="ts">
  import { onMount } from "svelte";
  import { initEventListeners, loadDownloads } from "./lib/state/downloads.svelte";
  import { getConfig, loadConfig } from "./lib/state/config.svelte";
  import Toolbar from "./lib/components/Toolbar.svelte";
  import SearchBar from "./lib/components/SearchBar.svelte";
  import DownloadList from "./lib/components/DownloadList.svelte";
  import StatusBar from "./lib/components/StatusBar.svelte";
  import AddDownloadDialog from "./lib/components/AddDownloadDialog.svelte";
  import SettingsDialog from "./lib/components/SettingsDialog.svelte";

  let showAddDialog = $state(false);
  let showSettings = $state(false);

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
    }

    return () => mq.removeEventListener("change", handler);
  });

  // Re-apply theme when config changes (e.g. after saving settings)
  $effect(() => {
    const cfg = getConfig();
    if (cfg) applyTheme(cfg.theme || "system");
  });
</script>

<main class="flex flex-col h-screen bg-gray-50 dark:bg-gray-900 text-gray-900 dark:text-gray-100 select-none">
  <Toolbar
    onAdd={() => (showAddDialog = true)}
    onSettings={() => (showSettings = true)}
  />
  <SearchBar />
  <div class="flex-1 overflow-y-auto">
    <DownloadList />
  </div>
  <StatusBar />
</main>

{#if showAddDialog}
  <AddDownloadDialog onClose={() => (showAddDialog = false)} />
{/if}

{#if showSettings}
  <SettingsDialog onClose={() => (showSettings = false)} />
{/if}
