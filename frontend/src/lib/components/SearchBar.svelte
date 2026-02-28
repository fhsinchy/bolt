<script lang="ts">
  import { getSearchQuery, setSearchQuery } from "../state/downloads.svelte";

  let value = $derived(getSearchQuery());

  function handleInput(e: Event) {
    setSearchQuery((e.target as HTMLInputElement).value);
  }
</script>

<div class="px-4 py-2 bg-white border-b border-gray-200">
  <div class="relative">
    <svg
      class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
    >
      <circle cx="11" cy="11" r="8" />
      <line x1="21" y1="21" x2="16.65" y2="16.65" />
    </svg>
    <input
      type="text"
      placeholder="Search downloads..."
      {value}
      oninput={handleInput}
      class="w-full pl-9 pr-3 py-1.5 text-sm bg-gray-50 border border-gray-200 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
    />
  </div>
</div>
