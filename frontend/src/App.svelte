<script lang="ts">
  import { onMount } from "svelte";
  import { initEventListeners, loadDownloads } from "./lib/state/downloads.svelte";
  import { loadConfig } from "./lib/state/config.svelte";
  import Toolbar from "./lib/components/Toolbar.svelte";
  import SearchBar from "./lib/components/SearchBar.svelte";
  import DownloadList from "./lib/components/DownloadList.svelte";
  import StatusBar from "./lib/components/StatusBar.svelte";
  import AddDownloadDialog from "./lib/components/AddDownloadDialog.svelte";
  import SettingsDialog from "./lib/components/SettingsDialog.svelte";

  let showAddDialog = $state(false);
  let showSettings = $state(false);

  onMount(() => {
    initEventListeners();
    loadDownloads();
    loadConfig();
  });
</script>

<main class="flex flex-col h-screen bg-gray-50 text-gray-900 select-none">
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
