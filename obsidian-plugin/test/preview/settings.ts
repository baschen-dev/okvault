import { TFolder } from "obsidian";
import { TeamSettingsSection } from "../../src/teamSettings";
import { ConnectionsSection } from "../../src/connectionsSection";

const files: Record<string, string> = { ".okvault.yaml": await (await fetch("/okvault.yaml")).text() };
const plugin: any = {
  settings: { binaryPath: "okvault" },
  vaultDir: () => "vault",
  toObsidian: (p: string) => p,
  fromObsidian: (p: string) => p,
  loadTeamGit: async () => {},
  refreshViews: () => {},
  teamGit: { mode: "off" },
  app: {
    vault: {
      adapter: {
        exists: async (p: string) => p in files,
        read: async (p: string) => files[p],
        write: async (p: string, t: string) => { files[p] = t; console.log("WROTE", p, t); },
      },
      getAllLoadedFiles: () => ["/", "faq", "reference", "conventions", "_templates"].map((p) => new (TFolder as any)(p)),
    },
  },
};
(window as any).files = files;
const host = document.getElementById("settings")!;
const connEl = host.createDiv();
const team = new TeamSettingsSection(plugin);
await team.display(host.createDiv());
new ConnectionsSection(plugin, team).display(connEl);
