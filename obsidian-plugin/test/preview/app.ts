import { OkvaultClient } from "../../src/client";
import { FaqView } from "../../src/faqView";
import { BundleView } from "../../src/bundleView";
import { TeamView } from "../../src/teamView";
import { demoTeam } from "./team-demo";

const notes = await (await fetch("/notes.json")).json();
const fetchPost = async (url: string, headers: Record<string, string>, body: string) => {
  const r = await fetch(url, { method: "POST", headers, body });
  const h: Record<string, string> = {};
  r.headers.forEach((v, k) => (h[k] = v));
  return { status: r.status, headers: h, text: await r.text() };
};
const app = {
  workspace: { openLinkText: (h: string) => console.log("open", h) },
  metadataCache: { on: () => null },
  vault: { on: () => null },
};
const plugin: any = {
  client: new OkvaultClient("/mcp", fetchPost),
  settings: { serverUrl: "/mcp", graphHiddenTypes: [], graphHiddenSections: [] },
  saveSettings: async () => {},
  loadIgnore: async () => false,
  lastError: "",
  ensureConnected: async () => plugin.client.ping(),
  connect: async () => plugin.client.ping(),
  markDisconnected: (m: string) => (plugin.lastError = m),
  localNotes: () => notes,
  toObsidian: (p: string) => p,
  openNote: (p: string) => console.log("open note", p),
  teamGit: { mode: "off" },
  git: null,
  syncing: false,
  loadTeamGit: async () => {},
  refreshGit: async () => {},
  obsidianGitConflicts: () => ({ enabled: false, auto: [], pushEnabled: false }),
  openObsidianGitSettings: () => console.log("open obsidian git settings"),
  sync: async () => console.log("sync"),
  verify: async (p: string) => (console.log("verify", p), true),
};
const reasons = new URLSearchParams(location.search).get("demo") === "1";
if (reasons) {
  plugin.teamGit = demoTeam.teamGit;
  plugin.git = demoTeam.git;
  plugin.obsidianGitConflicts = () => demoTeam.obsidianGit;
  const orig = plugin.client.review.bind(plugin.client);
  plugin.client.review = async () => {
    const r = await orig();
    r.items.push(...demoTeam.reviewItems);
    r.total = r.items.length;
    return r;
  };
}
const faq = new FaqView({ app, el: document.getElementById("faq")! } as any, plugin);
const bundle = new BundleView({ app, el: document.getElementById("bundle")! } as any, plugin);
const team = new TeamView({ app, el: document.getElementById("team")! } as any, plugin);
await faq.onOpen();
await team.onOpen();
await bundle.onOpen();
(window as any).faq = faq;
