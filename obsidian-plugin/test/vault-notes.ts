// Reads a vault from disk into the bundle model's input, roughly as Obsidian's metadata cache would.
import { readFileSync, readdirSync, statSync, existsSync } from "node:fs";
import { join, relative, dirname, posix } from "node:path";
import type { NoteInput } from "../src/bundle.ts";
import { IGNORE_FILE, VaultIgnore } from "../src/paths.ts";

function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((f) => {
    const p = join(dir, f);
    return statSync(p).isDirectory() ? walk(p) : [p];
  });
}

/** A YAML scalar as Obsidian reads it: quoted strings lose their quotes. */
function unquote(v: string): string {
  if (v.startsWith('"') && v.endsWith('"') && v.length > 1) {
    try {
      return JSON.parse(v);
    } catch {
      return v.slice(1, -1);
    }
  }
  if (v.startsWith("'") && v.endsWith("'") && v.length > 1) return v.slice(1, -1).replace(/''/g, "'");
  return v;
}

export function readVaultNotes(vault: string): (NoteInput & { aliases: string[]; description: string })[] {
  const ignoreFile = join(vault, IGNORE_FILE);
  const ignore = new VaultIgnore(existsSync(ignoreFile) ? readFileSync(ignoreFile, "utf8") : "");
  const rel = (abs: string) => relative(vault, abs).split("\\").join("/");
  return walk(vault)
    .filter((p) => p.endsWith(".md") && !relative(vault, p).startsWith(".") && !ignore.match(rel(p)))
    .map((abs) => {
      const path = rel(abs);
      const text = readFileSync(abs, "utf8").replace(/\r\n/g, "\n");
      const fm = /^---\n([\s\S]*?)\n---/.exec(text)?.[1] ?? "";
      const get = (k: string) => unquote(new RegExp(`^${k}:\s*(.+)$`, "m").exec(fm)?.[1].trim() ?? "");
      const targets = [...text.matchAll(/\]\(([^)#\s]+\.md)\)/g)].map((m) =>
        m[1].startsWith("/") ? m[1].slice(1) : posix.normalize(posix.join(dirname(path), m[1])),
      );
      const exists = targets.filter((t) => existsSync(join(vault, t)));
      const links = exists.filter((t) => !ignore.match(t));
      return {
        path,
        title: get("title") || path.split("/").pop()!.replace(/\.md$/, ""),
        type: get("type"),
        status: get("status"),
        description: get("description"),
        aliases: [],
        links,
        broken: targets.length - exists.length,
      };
    });
}
