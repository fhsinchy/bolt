<script lang="ts">
  import type { Download } from "../types";
  import { formatBytes, formatSpeed, formatETA, truncateFilename, fileExtension } from "../utils/format";
  import ProgressBar from "./ProgressBar.svelte";
  import ActionButtons from "./ActionButtons.svelte";
  import { getSelectedIds, toggleSelected } from "../state/downloads.svelte";

  interface Props {
    download: Download;
  }

  let { download }: Props = $props();

  const selected = $derived(getSelectedIds().has(download.id));

  const statusBadge = $derived.by(() => {
    switch (download.status) {
      case "completed":
        return { text: "Completed", class: "bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300" };
      case "active":
        return { text: "Downloading", class: "bg-blue-100 text-blue-700 dark:bg-blue-900 dark:text-blue-300" };
      case "queued":
        return { text: "Queued", class: "bg-yellow-100 text-yellow-700 dark:bg-yellow-900 dark:text-yellow-300" };
      case "error":
        return { text: "Error", class: "bg-red-100 text-red-700 dark:bg-red-900 dark:text-red-300" };
      case "paused":
        return { text: "Paused", class: "bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300" };
      case "refresh":
        return { text: "Refresh", class: "bg-orange-100 text-orange-700 dark:bg-orange-900 dark:text-orange-300" };
      default:
        return { text: download.status, class: "bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300" };
    }
  });

  const ext = $derived(fileExtension(download.filename));

  const fileIcon = $derived.by(() => {
    const videoExts = ["mp4", "mkv", "avi", "mov", "wmv", "flv", "webm"];
    const audioExts = ["mp3", "flac", "wav", "aac", "ogg", "m4a"];
    const imageExts = ["jpg", "jpeg", "png", "gif", "bmp", "svg", "webp"];
    const archiveExts = ["zip", "tar", "gz", "bz2", "xz", "7z", "rar", "zst"];
    const docExts = ["pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "txt"];

    if (videoExts.includes(ext)) return "🎬";
    if (audioExts.includes(ext)) return "🎵";
    if (imageExts.includes(ext)) return "🖼";
    if (archiveExts.includes(ext)) return "📦";
    if (docExts.includes(ext)) return "📄";
    if (["exe", "msi", "dmg", "deb", "rpm", "appimage"].includes(ext))
      return "⚙";
    if (["iso", "img"].includes(ext)) return "💿";
    return "📁";
  });

  function handleClick() {
    toggleSelected(download.id);
  }

  async function handleDblClick() {
    if (download.status === "completed") {
      try {
        const path = download.dir + "/" + download.filename;
        await (window as any).go.app.App.OpenFile(path);
      } catch (e) {
        console.error("Open file failed:", e);
      }
    }
  }

  const sizeText = $derived.by(() => {
    if (download.total_size > 0) {
      return `${formatBytes(download.downloaded)} / ${formatBytes(download.total_size)}`;
    }
    if (download.downloaded > 0) {
      return formatBytes(download.downloaded);
    }
    return "Unknown size";
  });
</script>

<div
  class="flex items-center gap-3 px-4 py-3 border-b border-gray-100 dark:border-gray-700 hover:bg-gray-100 dark:hover:bg-gray-800 cursor-default transition-colors {selected
    ? 'bg-blue-50 dark:bg-blue-900/30'
    : ''}"
  role="button"
  tabindex="0"
  onclick={handleClick}
  ondblclick={handleDblClick}
  onkeydown={(e) => e.key === "Enter" && handleClick()}
>
  <!-- File icon -->
  <span class="text-lg w-6 text-center flex-shrink-0">{fileIcon}</span>

  <!-- File info -->
  <div class="flex-1 min-w-0">
    <div class="flex items-center gap-2">
      <span class="text-sm font-medium truncate" title={download.filename}>
        {truncateFilename(download.filename)}
      </span>
      <span
        class="text-[10px] px-1.5 py-0.5 rounded-full font-medium flex-shrink-0 {statusBadge.class}"
      >
        {statusBadge.text}
      </span>
    </div>

    <div class="mt-1">
      <ProgressBar
        downloaded={download.downloaded}
        totalSize={download.total_size}
        status={download.status}
      />
    </div>

    <div class="flex items-center gap-3 mt-1 text-xs text-gray-500 dark:text-gray-400">
      <span>{sizeText}</span>
      {#if download.status === "active" && download.speed}
        <span>{formatSpeed(download.speed)}</span>
      {/if}
      {#if download.status === "active" && download.eta}
        <span>ETA {formatETA(download.eta)}</span>
      {/if}
      {#if download.status === "error" && download.error}
        <span class="text-red-500 truncate" title={download.error}>
          {download.error}
        </span>
      {/if}
    </div>
  </div>

  <!-- Action buttons -->
  <div class="flex-shrink-0">
    <ActionButtons {download} />
  </div>
</div>
