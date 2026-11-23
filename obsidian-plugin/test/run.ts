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
import { OkvaultProcess, syncNow, verifyNote } from "../src/server.ts";
import { buildBundle } from "../src/bundle.ts";
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
proc.start({ binary: BIN, vaultDir: VAULT, host: "127.0.0.1", port: PORT, versioning: false });

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

    // A server restart invalidates the session; the client must re-initialize.
    (c as unknown as { session: string }).session = "STALE-SESSION";
    const again = await c.ask("how do I add an faq");
    check("re-initializes after a lost session", again.confident);
    check("read-only server reports no git sync", r.git === undefined);
  }
} finally {
  proc.stop();
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
  try {
    rmSync(TMP, { recursive: true, force: true });
  } catch {
    /* Windows may still hold a handle; the OS cleans temp */
  }
}

console.log(failures ? `\n${failures} failed` : "\nall passed");
process.exit(failures ? 1 : 0);
