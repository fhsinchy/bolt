const units = ["B", "KB", "MB", "GB", "TB"];

export function formatBytes(n: number): string {
  if (n <= 0) return "0 B";
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), units.length - 1);
  const val = n / Math.pow(1024, i);
  return `${val < 10 ? val.toFixed(1) : Math.round(val)} ${units[i]}`;
}

export function formatSpeed(bytesPerSec: number): string {
  if (bytesPerSec <= 0) return "0 B/s";
  return `${formatBytes(bytesPerSec)}/s`;
}

export function formatETA(seconds: number): string {
  if (seconds <= 0) return "--";
  if (seconds > 86400) return "> 1d";
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  if (h > 0) return `${h}h${m}m`;
  if (m > 0) return `${m}m${s}s`;
  return `${s}s`;
}

export function formatDate(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  return d.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function truncateFilename(name: string, max: number = 40): string {
  if (name.length <= max) return name;
  const ext = name.lastIndexOf(".");
  if (ext > 0 && name.length - ext <= 8) {
    const extStr = name.slice(ext);
    const base = name.slice(0, max - extStr.length - 3);
    return `${base}...${extStr}`;
  }
  return name.slice(0, max - 3) + "...";
}

export function fileExtension(filename: string): string {
  const dot = filename.lastIndexOf(".");
  if (dot < 0) return "";
  return filename.slice(dot + 1).toLowerCase();
}
