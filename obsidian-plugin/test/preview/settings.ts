import { TFolder } from "obsidian";
import { TeamSettingsSection } from "../../src/teamSettings";

const files: Record<string, string> = { ".okvault.yaml": await (await fetch("/okvault.yaml")).text() };
const plugin: any = {
  settings: { binaryPath: "okvault" },
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
await new TeamSettingsSection(plugin).display(document.getElementById("settings")!);
