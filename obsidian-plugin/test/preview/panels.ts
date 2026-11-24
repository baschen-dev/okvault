// Preview of the note inspector and the ticket workspace with a fake Obsidian app over the repo vault.
import { TFile as RealTFile } from "obsidian";
const TFile = RealTFile as unknown as new (p: string) => RealTFile;
type TFile = RealTFile;
import { OkvaultClient } from "../../src/client";
import { InspectorView } from "../../src/inspectorView";
import { TicketView } from "../../src/ticketView";
import { GapsView } from "../../src/gapsView";
import { lintVault } from "./server-browser";

type Note = { path: string; title: string; type: string; fm: Record<string, unknown>; links: string[]; broken: number };
const notes: Note[] = await (await fetch("/notes.json")).json();
// Demo trust and tickets on one note, as an agent edit checked earlier by a person.
const demo = notes.find((n) => n.path === "faq/how-do-i-add-an-faq.md")!;
Object.assign(demo.fm, {
  generated: { by: "claude-code", at: "2026-11-22T10:00:00Z" },
  verified: [{ by: "bob", at: "2026-10-02T09:00:00Z" }],
  stale_after: "2026-12-01T09:00:00Z",
  tickets: ["PROJ-123"],
});
notes.find((n) => n.path === "reference/search-ranking.md")!.fm.tickets = ["PROJ-123", "OPS-7"];
const files = new Map(notes.map((n) => [n.path, new TFile(n.path)]));
const tmpl = ["_templates/Decision.md", "_templates/FAQ.md", "_templates/Concept.md"].map((p) => new TFile(p));
const fetchPost = async (url: string, headers: Record<string, string>, body: string) => {
  const r = await fetch(url, { method: "POST", headers, body });
  const h: Record<string, string> = {};
  r.headers.forEach((v, k) => (h[k] = v));
  return { status: r.status, headers: h, text: await r.text() };
};
const active = files.get(new URLSearchParams(location.search).get("note") ?? demo.path)!;
const app: any = {
  workspace: { on: () => null, getActiveFile: () => active, getLeaf: () => ({ openFile: (f: TFile) => console.log("open", f.path) }) },
  metadataCache: { on: () => null, getFileCache: (f: TFile) => ({ frontmatter: notes.find((n) => n.path === f.path)?.fm ?? (f.path.startsWith("_templates/") ? { type: f.basename === "Concept" ? "Reference" : f.basename } : {}) }) },
  vault: {
    on: () => null,
    getMarkdownFiles: () => [...files.values(), ...tmpl],
    getAbstractFileByPath: (p: string) => files.get(p) ?? tmpl.find((t) => t.path === p) ?? null,
  },
  fileManager: { processFrontMatter: async (f: TFile, fn: (fm: any) => void) => { fn(notes.find((n) => n.path === f.path)!.fm); console.log("frontmatter", f.path) } },
};
const plugin: any = {
  app,
  client: new OkvaultClient("/mcp", fetchPost),
  settings: { binaryPath: "okvault" },
  lastError: "",
  lint: { findings: new Map(), error: "", at: 0, running: true },
  ticketProjects: ["PROJ", "OPS"],
  ensureConnected: async () => plugin.client.ping(),
  fromObsidian: (p: string) => p,
  toObsidian: (p: string) => p,
  localNotes: () => notes.map((n) => ({ ...n, aliases: [], description: "" })),
  openNote: (p: string) => console.log("open note", p),
  openTicket: (k: string) => ticket.show(k),
  verify: async (p: string) => (console.log("verify", p), true),
  requestLint: async () => {
    const r = await lintVault("okvault", "");
    plugin.lint = { findings: r.findings, error: r.error ?? "", at: Date.now(), running: false };
    insp.refresh();
  },
};
const insp = new InspectorView({ app, el: document.getElementById("bundle")! } as any, plugin);
const ticket = new TicketView({ app, el: document.getElementById("faq")! } as any, plugin);
await insp.onOpen();
await ticket.onOpen();
const gaps = new GapsView({ app, el: document.getElementById("team")! } as any, plugin);
await gaps.onOpen();
(window as any).gaps = gaps;
(window as any).ticket = ticket;
