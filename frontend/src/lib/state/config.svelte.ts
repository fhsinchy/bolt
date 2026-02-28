import type { Config } from "../types";

let config = $state<Config | null>(null);

export function getConfig() {
  return config;
}

export async function loadConfig() {
  try {
    const result = await (window as any).go.app.App.GetConfig();
    config = result;
  } catch (e) {
    console.error("Failed to load config:", e);
  }
}

export async function saveConfig(updates: Partial<Config>) {
  try {
    await (window as any).go.app.App.UpdateConfig(updates);
    await loadConfig();
  } catch (e) {
    console.error("Failed to save config:", e);
    throw e;
  }
}
