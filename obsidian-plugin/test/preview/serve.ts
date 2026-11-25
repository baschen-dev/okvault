// Preview the plugin's views in a browser: `node --experimental-strip-types test/preview/serve.ts`
// Serves the views (with an Obsidian API stub) on :8790 and proxies /mcp to a local okvault.
import { createServer } from "node:http";
import { spawn } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import esbuild from "esbuild";
import { readVaultNotes } from "../vault-notes.ts";
import { checkConfig, checkConnections, lintVault } from "../../src/server.ts";
import { parse as parseYaml } from "yaml";

const here = import.meta.dirname;
const root = join(here, "..", "..");
const vault = process.env.OKVAULT_VAULT ?? join(root, "..", "vault");
const okPort = 8791;
const bin = process.env.OKVAULT_BIN;
const [cmd, args] = bin ? [bin, [] as string[]] : ["go", ["run", "./cmd/okvault"]];
// A demo knowledge-gaps log, so the gaps panel has something to show.
const gapsFile = join(tmpdir(), "okvault-preview-gaps.jsonl");
const at = (h: number) => new Date(Date.now() - h * 3600_000).toISOString();
writeFileSync(gapsFile, [
  { at: at(30), q: "how do we rotate the jira api token", client: "claude-code" },
  { at: at(20), q: "How do we rotate the Jira API token?", client: "cursor" },
  { at: at(5), q: "rotate jira api token steps", client: "claude-code" },
  { at: at(26), q: "how do we cut a hotfix release", client: "claude-code" },
  { at: at(2), q: "what does okvault do with draft faqs", client: "claude-code" },
  { at: at(1), q: "How do I write a FAQ?", client: "claude-code" },
].map((e) => JSON.stringify(e)).join("\n") + "\n");
const ok = spawn(cmd, [...args, "serve", "--vault", vault, "--port", String(okPort), "--read-only", "--git", "off", "--gaps", gapsFile], { cwd: join(root, ".."), stdio: "inherit" });
process.on("exit", () => ok.kill());
process.on("SIGINT", () => process.exit());

const build = async (entry: string) => (await esbuild.build({
  entryPoints: [join(here, entry)], bundle: true, format: "esm", write: false, target: "es2022",
  alias: { obsidian: join(here, "obsidian-stub.ts") },
  plugins: [{ name: "server-stub", setup(b) { b.onResolve({ filter: /^\.\/server$/ }, () => ({ path: join(here, "server-browser.ts") })); } }],
})).outputFiles[0].text;
const js = await build("app.ts");
const settingsJs = await build("settings.ts");
const panelsJs = await build("panels.ts");
const settingsTabJs = await build("settings-tab.ts");

const html = `<!doctype html><html><head><meta charset="utf-8"><title>okvault plugin preview</title>
<style>
body{margin:0;font-family:system-ui,"Segoe UI",sans-serif;display:grid;grid-template-columns:1fr 320px 320px;height:100vh;
--background-primary:#fff;--background-secondary:#f6f6f6;--background-modifier-border:#e0e0e0;--background-modifier-hover:rgba(0,0,0,.05);
--text-normal:#222;--text-muted:#5c5c5c;--text-faint:#999;--text-accent:#705dcf;--text-error:#d03b3b;--interactive-accent:#7b6cd9;
--font-ui-smaller:12px;--font-ui-small:13px;--font-semibold:600;--font-medium:500;--font-normal:400;--font-monospace:ui-monospace,monospace;
--radius-s:4px;--radius-m:8px;--shadow-s:0 2px 8px rgba(0,0,0,.12);color:var(--text-normal);background:var(--background-primary)}
body.theme-dark{--background-primary:#1e1e1e;--background-secondary:#262626;--background-modifier-border:#363636;--background-modifier-hover:rgba(255,255,255,.06);
--text-normal:#dadada;--text-muted:#a3a3a3;--text-faint:#666;--text-accent:#a99bf0;--interactive-accent:#8a7cf0}
#bundle{overflow:auto} #faq{overflow:auto;background:var(--background-secondary);border-left:1px solid var(--background-modifier-border)}
button{font:inherit;cursor:pointer} button.mod-cta{background:var(--interactive-accent);color:#fff;border:none;border-radius:4px;padding:2px 10px} .clickable-icon{background:none;border:none;color:var(--text-muted)}
</style><link rel="stylesheet" href="/styles.css"></head>
<body class="${"$"}{THEME}"><div id="bundle"></div><div id="faq"></div><div id="team" style="overflow:auto;background:var(--background-secondary);border-left:1px solid var(--background-modifier-border)"></div><script type="module" src="/app.js"></script></body></html>`;

function frontmatter(file: string): Record<string, unknown> {
  const m = /^---\r?\n([\s\S]*?)\r?\n---/.exec(readFileSync(file, "utf8"));
  return m ? (parseYaml(m[1]) ?? {}) : {};
}

const settingsHtml = html
  .replace('<div id="bundle"></div><div id="faq"></div>', '<div id="settings" class="vertical-tab-content" style="grid-column:1/-1;max-width:760px;margin:0 auto;padding:16px;overflow:auto"></div>')
  .replace("/app.js", "/settings.js")
  .replace("</style>", ".setting-item{display:flex;gap:16px;align-items:center;padding:10px 0;border-top:1px solid var(--background-modifier-border)}.setting-item-info{flex:1}.setting-item-name{font-weight:500}.setting-item-description{color:var(--text-muted);font-size:13px}.setting-item-heading .setting-item-name{font-size:16px;font-weight:600}.setting-item-control{display:flex;gap:6px}input,select{font:inherit;padding:3px 6px}.preview-notice{position:fixed;top:10px;right:10px;background:#333;color:#fff;padding:8px 12px;border-radius:6px}</style>");

createServer(async (req, res) => {
  const url = new URL(req.url!, "http://x");
  if (url.pathname === "/mcp") {
    const body = await new Promise<string>((r) => { let b = ""; req.on("data", (c) => (b += c)); req.on("end", () => r(b)); });
    const h: Record<string, string> = {};
    for (const k of ["content-type", "accept", "mcp-session-id", "mcp-protocol-version"]) if (req.headers[k]) h[k] = String(req.headers[k]);
    const r = await fetch(`http://127.0.0.1:${okPort}/mcp`, { method: "POST", headers: h, body });
    const out: Record<string, string> = { "content-type": r.headers.get("content-type") ?? "text/plain" };
    const sid = r.headers.get("mcp-session-id");
    if (sid) out["mcp-session-id"] = sid;
    res.writeHead(r.status, out).end(await r.text());
  } else if (url.pathname === "/check-config") {
    const body = await new Promise<string>((r) => { let b = ""; req.on("data", (c) => (b += c)); req.on("end", () => r(b)); });
    res.writeHead(200, { "content-type": "application/json" }).end(JSON.stringify(await checkConfig(bin ?? "okvault", body)));
  } else if (url.pathname === "/okvault.yaml") res.writeHead(200).end(readFileSync(join(vault, ".okvault.yaml")));
  else if (url.pathname === "/lint") {
    const r = await lintVault(bin ?? "okvault", vault);
    res.writeHead(200, { "content-type": "application/json" }).end(JSON.stringify({ findings: Object.fromEntries(r.findings), error: r.error }));
  } else if (url.pathname === "/panels.js") res.writeHead(200, { "content-type": "text/javascript" }).end(panelsJs);
  else if (url.pathname === "/panels") res.writeHead(200, { "content-type": "text/html" }).end(html.replace("/app.js", "/panels.js").replace("${THEME}", ""));
  else if (url.pathname === "/connections") {
    // The real command on this vault (no push dry run); ?demo=1 adds a Jira site with projects.
    const r = await checkConnections(bin ?? "okvault", vault, { pushCheck: false });
    if (url.searchParams.get("demo") === "1" && r.result) {
      r.result.jira = {
        url: url.searchParams.get("jira") || "https://acme.atlassian.net", api: "3", credentials: "JIRA_API_TOKEN + JIRA_EMAIL", user: "Ada Lovelace",
        projects: [["DATA", "Data platform"], ["OPS", "Operations"], ["PROJ", "Product"], ["SEC", "Security"], ["WEB", "Website"]].map(([key, name]) => ({ key, name })),
      };
    }
    res.writeHead(200, { "content-type": "application/json" }).end(JSON.stringify(r));
  }
  else if (url.pathname === "/settings-tab.js") res.writeHead(200, { "content-type": "text/javascript" }).end(settingsTabJs);
  else if (url.pathname === "/settings-tab") res.writeHead(200, { "content-type": "text/html" }).end(settingsHtml.replace("/settings.js", "/settings-tab.js").replace("${THEME}", ""));
  else if (url.pathname === "/settings.js") res.writeHead(200, { "content-type": "text/javascript" }).end(settingsJs);
  else if (url.pathname === "/settings") res.writeHead(200, { "content-type": "text/html" }).end(settingsHtml);
  else if (url.pathname === "/app.js") res.writeHead(200, { "content-type": "text/javascript" }).end(js);
  else if (url.pathname === "/styles.css") res.writeHead(200, { "content-type": "text/css" }).end(readFileSync(join(root, "styles.css")));
  else if (url.pathname === "/notes.json") res.writeHead(200, { "content-type": "application/json" }).end(JSON.stringify(readVaultNotes(vault).map((n) => ({ ...n, fm: frontmatter(join(vault, n.path)) }))));
  else res.writeHead(200, { "content-type": "text/html" }).end(html.replace("${THEME}", url.searchParams.get("theme") === "dark" ? "theme-dark" : ""));
}).listen(8790, "127.0.0.1", () => console.log("preview on http://127.0.0.1:8790/  (?theme=dark, ?demo=1 adds review reasons)"));
