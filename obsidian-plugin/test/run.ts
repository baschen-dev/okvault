// Integration test: the plugin's MCP client against a real `okvault serve --port`
// started by the plugin's own process code, the bundle model against the
// repository's vault, and versioning (sync, verify) in a scratch git repository
// with a bare remote.
//
//   OKVAULT_BIN=/path/to/okvault npm test     (default: builds ../cmd/okvault)
import { execFileSync } from "node:child_process";
import { cpSync, mkdtempSync, readFileSync, appendFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { OkvaultClient, parseMessages, type Post } from "../src/client.ts";
import { OkvaultProcess, checkConfig, checkConnections, lintVault, syncNow, verifyNote } from "../src/server.ts";
import { fillTemplate, folderFor, kebab, mergeAliases, normalizeKey, notePath, relativeLink, ticketKeyAt, ticketsOf, trustOf } from "../src/noteFacts.ts";
import { applyTeamConfig, checkTeamConfig, emptyConfig, readTeamConfig, type TeamConfig } from "../src/teamConfig.ts";
import { buildBundle, catalog, filterNotes } from "../src/bundle.ts";
import { VaultIgnore, normalizeRoot } from "../src/paths.ts";
import { DEFAULT_SETTINGS, cleanSetting, rootProblem } from "../src/settingsModel.ts";
import { readVaultNotes } from "./vault-notes.ts";

const VAULT = join(import.meta.dirname, "..", "..", "vault");
const PORT = 18000 + Math.floor(Math.random() * 1000);
let failures = 0;

function check(name: string, ok: boolean, detail = ""): void {
  console.log(`${ok ? "ok  " : "FAIL"} ${name}${detail ? ` - ${detail}` : ""}`);
  if (!ok) failures++;
}

const fetchPost: Post = async (url, headers, body) => {
  const r = await fetch(url, { method: "POST", headers, body });
  const h: Record<string, string> = {};
  r.headers.forEach((v, k) => (h[k] = v));
  return { status: r.status, headers: h, text: await r.text() };
};

// ---- SSE parsing ----
const msgs = parseMessages('event: message\ndata: {"jsonrpc":"2.0","id":3,"result":{"x":1}}\n\n');
check("parse SSE frame", msgs.length === 1 && msgs[0].id === 3);
check("parse plain JSON", parseMessages('{"jsonrpc":"2.0","id":4,"result":{}}')[0].id === 4);

// ---- bundle model from the vault on disk ----
const notes = readVaultNotes(VAULT);
const b = buildBundle(notes, new Map([["faq/how-do-i-add-an-faq.md", ["unverified"]]]));
check("index.md and log.md are not concepts", ![...b.nodes.keys()].some((p) => /(^|\/)(index|log)\.md$/.test(p)));
check("_templates excluded", ![...b.nodes.keys()].some((p) => p.startsWith("_templates/")));
check("sections are top folders", b.sections.map((s) => s.name).join(",") === "conventions,faq,reference", b.sections.map((s) => s.name).join(","));
check("FAQ is a type", b.types.includes("FAQ"), b.types.join(","));
check("links found", b.edges.length > 0, `${b.edges.length} edges`);
check("review reason carried", b.nodes.get("faq/how-do-i-add-an-faq.md")!.reasons.has("unverified"));
const angles = [...b.nodes.values()].map((n) => n.angle);
check("angles in (0, 2π) and distinct", angles.every((a) => a > 0 && a < 2 * Math.PI) && new Set(angles).size === angles.length);

// ---- graph filters remove notes from the layout ----
{
  const cat = catalog(notes);
  check("catalog counts every concept", cat.total === b.nodes.size, `${cat.total} vs ${b.nodes.size}`);
  const noFaq = buildBundle(filterNotes(notes, { hiddenTypes: new Set(["FAQ"]), hiddenSections: new Set(), onlyReasons: new Set() }), new Map());
  check("hidden type leaves the layout", ![...noFaq.nodes.values()].some((n) => n.type === "FAQ") && noFaq.nodes.size === b.nodes.size - (cat.typeCount.get("FAQ") ?? 0));
  check("hidden type's links go too", noFaq.edges.every((e) => noFaq.nodes.has(e.from) && noFaq.nodes.has(e.to)));
  const angles2 = [...noFaq.nodes.values()].map((n) => n.angle).sort((x, y) => x - y);
  check("remaining notes take the whole circle", angles2.length > 0 && angles2[angles2.length - 1] - angles2[0] > Math.PI);
  const noSec = buildBundle(filterNotes(notes, { hiddenTypes: new Set(), hiddenSections: new Set(["faq"]), onlyReasons: new Set() }), new Map());
  check("hidden section has no arc", !noSec.sections.some((s) => s.name === "faq") && noSec.sections.length === b.sections.length - 1);
  check("hidden section's notes gone", ![...noSec.nodes.keys()].some((p) => p.startsWith("faq/")));
  // Review filters narrow the map to notes with one of the picked reasons.
  const rev = new Map([["faq/how-do-i-add-an-faq.md", ["unverified"]], ["conventions/x.md", ["stale"]]]);
  const fake = [
    ...notes,
    { path: "conventions/x.md", title: "X", type: "Reference", links: [], broken: 0 },
    { path: "conventions/y.md", title: "Y", type: "Reference", links: [], broken: 2, status: "draft" },
  ];
  const none = { hiddenTypes: new Set<string>(), hiddenSections: new Set<string>() };
  const only = (...rs: string[]) => filterNotes(fake, { ...none, onlyReasons: new Set(rs) }, rev).map((n) => n.path).sort().join(",");
  check("no review filter keeps every concept", filterNotes(fake, { ...none, onlyReasons: new Set() }, rev).length === catalog(fake).total);
  check("only stale", only("stale") === "conventions/x.md", only("stale"));
  check("only broken links (local)", only("broken-links").split(",").includes("conventions/y.md") && !only("broken-links").includes("conventions/x.md"));
  check("review reasons are ORed", only("stale", "unverified") === "conventions/x.md,faq/how-do-i-add-an-faq.md", only("stale", "unverified"));
  check("review filter combines with hidden sections", filterNotes(fake, { hiddenTypes: new Set(), hiddenSections: new Set(["conventions"]), onlyReasons: new Set(["stale"]) }, rev).length === 0);
  const rc = catalog(fake, rev).reasonCount;
  check("catalog counts review reasons", rc.get("stale") === 1 && rc.get("unverified") === 1 && (rc.get("draft") ?? 0) >= 1 && (rc.get("broken-links") ?? 0) >= 1, JSON.stringify([...rc]));
}

// ---- plugin settings form ----
{
  check("empty server URL falls back to the default", cleanSetting("serverUrl", "  ") === DEFAULT_SETTINGS.serverUrl);
  check("server URL is trimmed", cleanSetting("serverUrl", " http://x/mcp ") === "http://x/mcp");
  check("empty binary falls back to okvault", cleanSetting("binaryPath", "") === "okvault");
  check("toggle is a boolean", cleanSetting("autoStart", true) === true && cleanSetting("autoStart", "yes") === false);
  check("versioning is follow or off", cleanSetting("versioning", "off") === "off" && cleanSetting("versioning", "bogus") === "follow");
  check("root is normalized", cleanSetting("vaultFolder", "./vault/") === "vault" && cleanSetting("vaultFolder", "/") === "");
  check("root leaving the vault is not stored", cleanSetting("vaultFolder", "../x") === undefined);
  check("unknown keys are not stored", cleanSetting("graphHiddenTypes", ["FAQ"]) === undefined);
  const has = (p: string) => p === "vault";
  check("root problems", rootProblem("..", has).includes("inside") && rootProblem("nope", has).includes("No folder nope/") && rootProblem("vault/", has) === "" && rootProblem("", has) === "");
}

// ---- okvault root and .vaultignore ----
{
  for (const [input, want] of [["", ""], [".", ""], ["/", ""], ["./", ""], ["  vault/ ", "vault"], ["./docs/kb/", "docs/kb"], ["docs\\kb", "docs/kb"], ["a//b", "a/b"], ["../x", null], ["a/../b", null]] as const) {
    check(`normalizeRoot(${JSON.stringify(input)})`, normalizeRoot(input) === want, String(normalizeRoot(input)));
  }
  const fixture = JSON.parse(readFileSync(join(import.meta.dirname, "..", "..", "internal", "vault", "testdata", "vaultignore-cases.json"), "utf8"));
  let bad = 0;
  let total = 0;
  for (const g of fixture.groups) {
    const ig = new VaultIgnore(g.rules);
    for (const c of g.cases) {
      total++;
      if (ig.match(c.path, c.dir) !== c.ignored) {
        bad++;
        console.log(`     .vaultignore: match(${JSON.stringify(c.path)}, dir=${c.dir}) should be ${c.ignored}`);
      }
    }
  }
  check(".vaultignore matcher agrees with Go's cases", bad === 0 && total > 10, `${total - bad}/${total}`);
  const dir = mkdtempSync(join(tmpdir(), "okv-ignore-"));
  cpSync(VAULT, dir, { recursive: true });
  writeFileSync(join(dir, ".vaultignore"), "reference/\n");
  const kept = readVaultNotes(dir);
  check("ignored folder left out of the notes", !kept.some((n) => n.path.startsWith("reference/")) && kept.length > 0);
  check("links into ignored files are not broken", kept.every((n) => n.links.every((l) => !l.startsWith("reference/"))) && kept.reduce((s, n) => s + n.broken, 0) === notes.reduce((s, n) => s + n.broken, 0));
  rmSync(dir, { recursive: true, force: true });
}

// ---- note facts (inspector, ticket workspace) ----
{
  const now = new Date("2026-11-24T12:00:00Z");
  const t1 = trustOf({ generated: { by: "claude-code", at: "2026-11-20T10:00:00Z" } }, now);
  check("agent note never checked is unverified", t1.unverified && !t1.lastVerified && t1.generated?.by === "claude-code");
  const t2 = trustOf(
    {
      generated: { by: "claude-code", at: "2026-11-20T10:00:00Z" },
      verified: [{ by: "bob", at: "2026-11-21T09:00:00Z" }, { by: "alice", at: "2026-11-01T09:00:00Z" }],
      stale_after: "2027-05-20T09:00:00Z",
    },
    now,
  );
  check("a later check clears unverified; last check is the newest", !t2.unverified && t2.lastVerified?.by === "bob" && t2.verified.length === 2);
  check("days until stale", t2.daysLeft === 177 && !t2.stale, String(t2.daysLeft));
  const t3 = trustOf({ generated: { by: "a", at: "2026-11-22T00:00:00Z" }, verified: { by: "bob", at: "2026-11-21T00:00:00Z" }, stale_after: "2026-10-01" }, now);
  check("agent edit after the check, past stale_after", t3.unverified && t3.stale && t3.daysLeft! < 0);
  check("unparseable stale_after is reported", trustOf({ stale_after: "soon" }, now).staleAfterInvalid);
  check("ticket key under the cursor", ticketKeyAt("see PROJ-12 and OPS-3", 6) === "PROJ-12" && ticketKeyAt("see PROJ-12 and OPS-3", 19, ["OPS"]) === "OPS-3");
  check("only configured projects count", ticketKeyAt("UTF-8 text", 2, ["PROJ"]) === null);
  check("normalizes typed keys", normalizeKey(" proj-7 ") === "PROJ-7" && normalizeKey("proj 7") === null && normalizeKey("PROJ-0") === null);
  check("tickets from frontmatter", ticketsOf({ tickets: ["proj-1", "nope", "OPS-2"] }).join() === "PROJ-1,OPS-2" && ticketsOf({ tickets: "A-1, B-2" }).length === 2);
  check("kebab-case names", kebab("Why we pinned the Jira API to v2?") === "why-we-pinned-the-jira-api-to-v2" && kebab("Café Ünïcode") === "cafe-unicode");
  const sample = [{ path: "faq/a.md", type: "FAQ" }, { path: "faq/b.md", type: "FAQ" }, { path: "decisions/x.md", type: "Decision" }, { path: "_templates/Decision.md", type: "Decision" }, { path: "_templates/Playbook.md", type: "Playbook" }];
  check("folder suggestion follows where a type lives", folderFor("FAQ", sample) === "faq" && folderFor("decision", sample) === "decisions" && folderFor("Playbook", sample) === "");
}

// ---- knowledge gaps helpers ----
{
  check("aliases merge without duplicates of each other or the title",
    mergeAliases(["Rotate token"], ["How do we rotate the Jira token?", "rotate token", "how do we rotate the jira token", "New way?"], "How do we rotate the Jira token").join(" | ") ===
      "Rotate token | New way?");
  check("aliases from a scalar or nothing", mergeAliases("one", ["two"], "t").join() === "one,two" && mergeAliases(undefined, ["x"], "t").join() === "x");
  check("relative links between notes", relativeLink("faq/a.md", "reference/b.md") === "../reference/b.md" && relativeLink("faq/a.md", "faq/c.md") === "./c.md" && relativeLink("a.md", "faq/c.md") === "./faq/c.md");
  const faqTemplate = '---\ntype: FAQ\ntitle: "{{title}}?"\naliases: []\n---\n\nAnswer about {{title}}.\n';
  const filled = fillTemplate(faqTemplate, "FAQ", 'What is "okvault" $1?');
  check("template title escaped in YAML, as typed in the body, one question mark",
    filled.includes('title: "What is \\"okvault\\" $1?"') && filled.includes('Answer about What is "okvault" $1.'), filled);
  check("note path from folder and title", notePath("faq/", "How do we rotate the Jira token?") === "faq/how-do-we-rotate-the-jira-token.md" && notePath("", "!!") === null);
}

// ---- live server, started the way the plugin starts it ----
const TMP = mkdtempSync(join(tmpdir(), "okvault-plugin-"));
const exe = process.platform === "win32" ? ".exe" : "";
const BIN = process.env.OKVAULT_BIN ?? join(TMP, `okvault${exe}`);
if (!process.env.OKVAULT_BIN) {
  execFileSync("go", ["build", "-o", BIN, "./cmd/okvault"], { cwd: join(import.meta.dirname, "..", ".."), stdio: "inherit" });
}
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

async function up(c: OkvaultClient): Promise<boolean> {
  for (let i = 0; i < 80; i++) {
    if (await c.ping()) return true;
    await sleep(250);
  }
  return false;
}

const proc = new OkvaultProcess();
const GAPS = join(TMP, "gaps.jsonl");
const at = (m: number) => new Date(Date.UTC(2026, 10, 24, 9, m)).toISOString();
writeFileSync(GAPS, [
  { at: at(0), q: "how do we rotate the jira api token", client: "claude-code" },
  { at: at(1), q: "How do we rotate the Jira API token?", client: "cursor" },
  { at: at(2), q: "How do I write a FAQ?", client: "claude-code" }, // an alias of faq/how-do-i-add-an-faq.md
  { at: at(3), q: "what colour is the deploy pipeline", client: "claude-code" },
].map((e) => JSON.stringify(e)).join("\n") + "\n");
proc.start({ binary: BIN, vaultDir: VAULT, host: "127.0.0.1", port: PORT, versioning: false, gapsFile: GAPS });

try {
  const c = new OkvaultClient(`http://127.0.0.1:${PORT}/mcp`, fetchPost);
  const ok = await up(c);
  check("server answers initialize", ok, ok ? "" : proc.exitInfo || proc.lastLog());
  if (ok) {
    const a = await c.ask("how do I add an faq");
    check("kb_ask confident", a.confident && a.path === "faq/how-do-i-add-an-faq.md", JSON.stringify({ c: a.confidence, p: a.path }));
    const miss = await c.ask("what colour is the deployment pipeline on fridays");
    check("kb_ask not confident on unrelated question", !miss.confident);
    const faqs = await c.search({ query: "", type: "FAQ", limit: 50 });
    check("kb_search type FAQ lists FAQs", faqs.length >= 4 && faqs.every((h) => h.type === "FAQ"), `${faqs.length}`);
    const hits = await c.search({ query: "bm25 ranking" });
    check("kb_search finds search-ranking", hits.some((h) => h.path === "reference/search-ranking.md"));
    const paths = await c.paths();
    check("kb_list returns this vault's paths", paths.includes("faq/how-do-i-add-an-faq.md"), `${paths.length}`);
    const r = await c.review(200);
    check("kb_review returns items array", Array.isArray(r.items) && typeof r.total === "number", `total ${r.total}`);
    const rs = await c.rescan();
    check("kb_review rescan re-reads the vault", rs.total === r.total && rs.items.length <= 1, `total ${rs.total} vs ${r.total}`);

    // A server restart invalidates the session; the client must re-initialize.
    (c as unknown as { session: string }).session = "STALE-SESSION";
    const again = await c.ask("how do I add an faq");
    check("re-initializes after a lost session", again.confident);
    check("read-only server reports no git sync", r.git === undefined);

    const d = await c.get("faq/how-do-i-add-an-faq.md");
    check("kb_get returns links and revision", d.type === "FAQ" && !!d.revision && (d.links ?? []).includes("conventions/note-format.md"), JSON.stringify(d.links));
    const back = await c.get("conventions/note-format.md");
    check("kb_get returns backlinks", (back.backlinks ?? []).includes("faq/how-do-i-add-an-faq.md"), JSON.stringify(back.backlinks));
    const tk = await c.ticket("proj-123");
    check("kb_ticket without a tracker still answers", tk.key === "PROJ-123" && !tk.ticket && !!tk.hint, JSON.stringify({ note: tk.tracker_note, hint: tk.hint }).slice(0, 160));
    let bad = "";
    try {
      await c.ticket("not a key");
    } catch (e) {
      bad = (e as Error).message;
    }
    check("kb_ticket rejects a non-key", bad.includes("not a ticket key"), bad);

    const g = await c.gaps();
    const top = g.gaps[0];
    check("kb_gaps: most asked first, phrasings grouped", g.total === 2 && top?.asked === 2 && top.question.toLowerCase().includes("rotate the jira api token") && top.clients?.join() === "claude-code,cursor", JSON.stringify(g).slice(0, 200));
    check("kb_gaps: a question an FAQ answers now is not a gap", g.answered === 1, String(g.answered));
    const lines = () => readFileSync(GAPS, "utf8").trim().split("\n").length;
    const before = lines();
    await c.ask("how do we rotate the jira api tok");
    check("the plugin's own questions are not recorded", lines() === before);
    const after = await c.gaps({ dismiss: top.id });
    check("dismiss hides a gap", after.total === 1 && after.dismissed === 1 && after.gaps[0].id !== top.id);
    check("kb_gaps reports where the log is", after.log.replace(/\\/g, "/").endsWith("gaps.jsonl"), after.log);

    const clean = await lintVault(BIN, VAULT);
    check("lint: the repo vault is clean", !clean.error && clean.findings.size === 0, clean.error ?? [...clean.findings.keys()].join());
    const lv = join(TMP, "lintvault");
    cpSync(join(VAULT, "faq"), join(lv, "faq"), { recursive: true });
    writeFileSync(join(lv, "faq", "Bad Name.md"), "---\ntitle: Untyped?\n---\nsee [x](./missing.md)\n");
    const dirty = await lintVault(BIN, lv);
    const f = dirty.findings.get("faq/Bad Name.md") ?? [];
    check("lint: findings grouped by note path", f.some((x) => x.message.includes("no `type`")) && f.some((x) => x.message.startsWith("broken link")) && f.some((x) => x.message.startsWith("file name")), JSON.stringify(f.map((x) => x.message)));
  }
} finally {
  proc.stop();
}

// ---- team settings editor (.okvault.yaml) ----
{
  const src = readFileSync(join(VAULT, ".okvault.yaml"), "utf8");
  const { config, error } = readTeamConfig(src);
  check("reads the vault's .okvault.yaml", !error && config.git.mode === "off", error);
  check("no change leaves the file byte for byte", applyTeamConfig(src, config) === src);
  config.git.mode = "pr";
  config.git.commit_edits = "10m";
  config.owners.push({ folder: "faq/", owners: ["team-docs"] });
  const edited = applyTeamConfig(src, config);
  check("edit keeps every comment", src.split("\n").filter((l) => l.startsWith("#")).every((l) => edited.includes(l)));
  check("edit writes the settings", /git:\n  mode: pr\n  commit_edits: 10m/.test(edited) && edited.includes("faq: [team-docs]"), edited.slice(-80));
  const back = readTeamConfig(edited).config;
  back.git.mode = "off";
  back.git.commit_edits = "";
  back.owners = [];
  check("removing them restores the original file", applyTeamConfig(edited, back) === src);

  const user = "# ours\ngit:\n  mode: push # policy\n  base: main\nowners:\n  ops: [team-ops] # on call\n  .: [docs]\n";
  const u = readTeamConfig(user).config;
  u.owners[1].owners = ["docs", "alice"];
  const out = applyTeamConfig(user, u);
  check("untouched keys and their comments stay as written", out.includes("mode: push # policy") && out.includes("ops: [team-ops] # on call") && out.includes(".: [docs, alice]"), out);
}

// Instant checks agree with okvault itself on what it accepts.
{
  const cases: [string, (c: TeamConfig) => void][] = [
    ["valid pr policy", (c) => Object.assign(c.git, { mode: "pr", base: "main", commit_edits: "10m", pull_interval: "5m" })],
    ["bad git mode name", (c) => Object.assign(c.git, { mode: "push", base: "bad..name" })],
    ["remote URL", (c) => Object.assign(c.git, { mode: "push", remote: "git@github.com/x" })],
    ["year interval", (c) => (c.review_interval = "1y")],
    ["zero pull interval", (c) => Object.assign(c.git, { mode: "push", pull_interval: "0" })],
    ["weeks interval", (c) => (c.review_interval = "26w")],
    ["jira without url", (c) => (c.tracker.kind = "jira")],
    ["jira cloud", (c) => Object.assign(c.tracker, { kind: "jira", url: "https://acme.atlassian.net", projects: ["PROJ", "ops"] })],
    ["bad project key", (c) => Object.assign(c.tracker, { kind: "none", url: "https://x.example", projects: ["1BAD"] })],
    ["credentials in url", (c) => Object.assign(c.tracker, { kind: "jira", url: "https://me:pw@acme.atlassian.net" })],
  ];
  let agree = 0;
  const disagreements: string[] = [];
  for (const [name, edit] of cases) {
    const c = emptyConfig();
    edit(c);
    const local = checkTeamConfig(c).size === 0;
    const remote = (await checkConfig(BIN, applyTeamConfig("", c))).errors;
    if (local === (remote.length === 0)) agree++;
    else disagreements.push(`${name}: plugin ${local ? "accepts" : "rejects"}, okvault ${remote.length ? "rejects: " + remote[0] : "accepts"}`);
  }
  check("instant checks agree with okvault", agree === cases.length, disagreements.join(" | "));
  const unknown = await checkConfig(BIN, "git:\n  mode: push\ntoken: abc\n");
  check("okvault check reports unknown keys", unknown.errors.some((e) => e.includes("token")), unknown.errors.join("; "));
}

// ---- versioning: a scratch repository with a bare remote, git.mode push ----
const git = (cwd: string, ...a: string[]) => execFileSync("git", a, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim();
const remote = join(TMP, "remote.git");
git(TMP, "init", "-q", "--bare", "-b", "main", remote);
const work = join(TMP, "alice");
git(TMP, "clone", "-q", remote, work);
git(work, "config", "user.name", "alice");
git(work, "config", "user.email", "alice@example.com");
git(work, "checkout", "-q", "-b", "main");
const vault = join(work, "vault");
cpSync(VAULT, vault, { recursive: true, filter: (src) => !/[\\/]\.obsidian([\\/]|$)/.test(src) });
writeFileSync(join(vault, ".okvault.yaml"), "git:\n  mode: push\n  pull_interval: 5s\n  commit_edits: 1s\n");
git(work, "add", "-A");
git(work, "commit", "-qm", "init");
git(work, "push", "-q", "-u", "origin", "main");

const gport = PORT + 1;
const gproc = new OkvaultProcess();
gproc.start({ binary: BIN, vaultDir: vault, host: "127.0.0.1", port: gport, versioning: true });
try {
  const c = new OkvaultClient(`http://127.0.0.1:${gport}/mcp`, fetchPost);
  check("versioning server starts", await up(c), gproc.exitInfo);
  const r = await c.review(1);
  check("kb_review carries git sync state", r.git?.mode === "push", JSON.stringify(r.git));

  // An edit made in Obsidian is committed by okvault and pushed, with no Obsidian Git.
  appendFileSync(join(vault, "reference", "search-ranking.md"), "\nEdited in Obsidian.\n");
  let pushed = "";
  for (let i = 0; i < 60 && !pushed; i++) {
    await sleep(1000);
    git(work, "fetch", "-q");
    const log = git(work, "log", "--format=%s", "origin/main");
    if (log.includes("docs(kb): edit reference/search-ranking.md")) pushed = log.split("\n")[0];
  }
  check("edit committed by okvault and pushed", pushed !== "", pushed);

  const s = await syncNow(BIN, vault);
  check("sync now returns okvault's status", s.status?.mode === "push" && !s.status.problem, JSON.stringify(s));

  const v = await verifyNote(BIN, vault, "faq/how-do-i-add-an-faq.md", "alice");
  check("verify succeeds", v.ok, v.message);
  check("verify records the person", /verified:[\s\S]*by: alice/.test(readFileSync(join(vault, "faq", "how-do-i-add-an-faq.md"), "utf8")));
  git(work, "fetch", "-q");
  check("verify is committed and pushed", git(work, "log", "-1", "--format=%s", "origin/main").length > 0 &&
    git(work, "show", "origin/main:vault/faq/how-do-i-add-an-faq.md").includes("by: alice"),
    git(work, "log", "-3", "--format=%s", "origin/main").replace(/\n/g, " | "));
  check("no uncommitted changes left", git(work, "status", "--porcelain") === "", git(work, "status", "--porcelain"));

  const off = await syncNow(BIN, VAULT);
  check("sync on a git-off vault explains why", !!off.error && /git is off/.test(off.error), off.error);
} finally {
  gproc.stop();
  await sleep(500);
}

// ---- connections: okvault connections against a scratch repo and a fake Jira ----
{
  const { createServer } = await import("node:http");
  const TOKEN = "jira-secret-token-123";
  const jira = createServer((req, res) => {
    const auth = req.headers.authorization ?? "";
    if (auth !== "Basic " + Buffer.from("ada@example.com:" + TOKEN).toString("base64")) {
      res.writeHead(401).end('{"errorMessages":["You are not authenticated."]}');
      return;
    }
    const u = new URL(req.url!, "http://x");
    if (u.pathname === "/rest/api/3/myself") res.end('{"displayName":"Ada Lovelace"}');
    else if (u.pathname === "/rest/api/3/project/search")
      res.end(JSON.stringify({ values: [{ key: "OPS", name: "Operations" }, { key: "PROJ", name: "Product" }], isLast: true }));
    else res.writeHead(404).end("{}");
  });
  await new Promise<void>((r) => jira.listen(0, "127.0.0.1", () => r()));
  const jiraUrl = `http://127.0.0.1:${(jira.address() as { port: number }).port}`;

  const T2 = mkdtempSync(join(tmpdir(), "okvault-conn-"));
  const git2 = (cwd: string, ...a: string[]) => execFileSync("git", a, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim();
  git2(T2, "init", "-q", "--bare", "-b", "main", join(T2, "remote.git"));
  const w = join(T2, "kb");
  git2(T2, "clone", "-q", join(T2, "remote.git"), w);
  git2(w, "config", "user.name", "ada");
  git2(w, "config", "user.email", "ada@example.com");
  writeFileSync(join(w, "note.md"), "---\ntype: Reference\n---\nx\n");
  git2(w, "add", "-A");
  git2(w, "commit", "-qm", "init");

  const saved = { t: process.env.JIRA_API_TOKEN, e: process.env.JIRA_EMAIL };
  try {
    delete process.env.JIRA_API_TOKEN;
    delete process.env.JIRA_EMAIL;
    const none = await checkConnections(BIN, w);
    check("connections: no tracker configured, local remote, push works",
      !none.error && !none.result?.jira && none.result?.github.push === "ok" && /not on GitHub/.test(none.result?.github.api_problem ?? ""),
      JSON.stringify(none).slice(0, 220));
    const noCreds = await checkConnections(BIN, w, { jiraUrl, pushCheck: false });
    check("connections: missing Jira credentials are named, not guessed", noCreds.result?.jira?.credentials === "none" && /JIRA_API_TOKEN is not set/.test(noCreds.result?.jira?.problem ?? ""), JSON.stringify(noCreds.result?.jira));
    process.env.JIRA_API_TOKEN = TOKEN;
    process.env.JIRA_EMAIL = "ada@example.com";
    const ok = await checkConnections(BIN, w, { jiraUrl, jiraApi: "3", pushCheck: false });
    const j = ok.result?.jira;
    check("connections: signs in and lists Jira projects", j?.user === "Ada Lovelace" && j.projects?.map((p) => p.key).join() === "OPS,PROJ" && !j.problem, JSON.stringify(j));
    check("connections: the token never reaches the output", !JSON.stringify(ok).includes(TOKEN));
  } finally {
    if (saved.t === undefined) delete process.env.JIRA_API_TOKEN;
    else process.env.JIRA_API_TOKEN = saved.t;
    if (saved.e === undefined) delete process.env.JIRA_EMAIL;
    else process.env.JIRA_EMAIL = saved.e;
    jira.close();
    try {
      rmSync(T2, { recursive: true, force: true });
    } catch {
      /* Windows may still hold a handle */
    }
  }
}

try {
  rmSync(TMP, { recursive: true, force: true }); // after every section: BIN may live here
} catch {
  /* Windows may still hold a handle; the OS cleans temp */
}

console.log(failures ? `\n${failures} failed` : "\nall passed");
process.exit(failures ? 1 : 0);
