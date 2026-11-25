// The whole settings tab: /settings-tab draws it the Obsidian 1.13+ way, from
// getSettingDefinitions(); /settings-tab?legacy=1 calls display(), as older versions do.
import { TFolder } from "obsidian";
import { renderDefinitions } from "./obsidian-stub.ts";
import { OkvaultSettingTab, DEFAULT_SETTINGS } from "../../src/settings";

const files: Record<string, string> = { ".okvault.yaml": await (await fetch("/okvault.yaml")).text() };
const folders = ["/", "faq", "reference", "conventions", "_templates"];
const plugin: any = {
  settings: { ...DEFAULT_SETTINGS },
  saves: 0,
  async saveSettings() { this.saves++; console.log("SAVED", JSON.stringify(this.settings)); },
  statusText: () => `okvault: connected (preview) at ${plugin.settings.serverUrl}`,
  connect: async () => console.log("connect"),
  vaultDir: () => "vault",
  toObsidian: (p: string) => p,
  fromObsidian: (p: string) => p,
  loadTeamGit: async () => {},
  refreshViews: () => {},
  teamGit: { mode: "off" },
  app: {
    vault: {
      getAbstractFileByPath: (p: string) => (folders.includes(p) ? new (TFolder as any)(p) : null),
      adapter: {
        exists: async (p: string) => p in files,
        read: async (p: string) => files[p],
        write: async (p: string, t: string) => { files[p] = t; console.log("WROTE", p, t); },
      },
      getAllLoadedFiles: () => folders.map((p) => new (TFolder as any)(p)),
    },
  },
};
(window as any).plugin = plugin;
const tab = new OkvaultSettingTab(plugin.app, plugin);
const host = document.getElementById("settings")!;
if (new URLSearchParams(location.search).get("legacy") === "1") {
  host.appendChild(tab.containerEl);
  tab.display();
} else renderDefinitions(tab, host);
