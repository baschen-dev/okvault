// Preview the plugin's views in a browser: `node --experimental-strip-types test/preview/serve.ts`
// Serves the views (with an Obsidian API stub) on :8790 and proxies /mcp to a local okvault.
import { createServer } from "node:http";
import { spawn } from "node:child_process";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import esbuild from "esbuild";
import { readVaultNotes } from "../vault-notes.ts";

const here = import.meta.dirname;
const root = join(here, "..", "..");
const vault = process.env.OKVAULT_VAULT ?? join(root, "..", "vault");
const okPort = 8791;
const bin = process.env.OKVAULT_BIN;
const [cmd, args] = bin ? [bin, [] as string[]] : ["go", ["run", "./cmd/okvault"]];
const ok = spawn(cmd, [...args, "serve", "--vault", vault, "--port", String(okPort), "--read-only", "--git", "off"], { cwd: join(root, ".."), stdio: "inherit" });
process.on("exit", () => ok.kill());
process.on("SIGINT", () => process.exit());

const js = (await esbuild.build({
  entryPoints: [join(here, "app.ts")], bundle: true, format: "esm", write: false, target: "es2022",
  alias: { obsidian: join(here, "obsidian-stub.ts") },
})).outputFiles[0].text;

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
  } else if (url.pathname === "/app.js") res.writeHead(200, { "content-type": "text/javascript" }).end(js);
  else if (url.pathname === "/styles.css") res.writeHead(200, { "content-type": "text/css" }).end(readFileSync(join(root, "styles.css")));
  else if (url.pathname === "/notes.json") res.writeHead(200, { "content-type": "application/json" }).end(JSON.stringify(readVaultNotes(vault)));
  else res.writeHead(200, { "content-type": "text/html" }).end(html.replace("${THEME}", url.searchParams.get("theme") === "dark" ? "theme-dark" : ""));
}).listen(8790, "127.0.0.1", () => console.log("preview on http://127.0.0.1:8790/  (?theme=dark, ?demo=1 adds review reasons)"));
