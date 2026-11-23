// Reads a vault from disk into the bundle model's input, roughly as Obsidian's metadata cache would.
import { readFileSync, readdirSync, statSync, existsSync } from "node:fs";
import { join, relative, dirname, posix } from "node:path";
import type { NoteInput } from "../src/bundle.ts";

function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((f) => {
    const p = join(dir, f);
    return statSync(p).isDirectory() ? walk(p) : [p];
  });
}

export function readVaultNotes(vault: string): (NoteInput & { aliases: string[]; description: string })[] {
  return walk(vault)
    .filter((p) => p.endsWith(".md") && !relative(vault, p).startsWith("."))
    .map((abs) => {
      const path = relative(vault, abs).split("\\").join("/");
      const text = readFileSync(abs, "utf8").replace(/\r\n/g, "\n");
      const fm = /^---\n([\s\S]*?)\n---/.exec(text)?.[1] ?? "";
      const get = (k: string) => new RegExp(`^${k}:\s*(.+)$`, "m").exec(fm)?.[1].trim() ?? "";
      const targets = [...text.matchAll(/\]\(([^)#\s]+\.md)\)/g)].map((m) =>
        m[1].startsWith("/") ? m[1].slice(1) : posix.normalize(posix.join(dirname(path), m[1])),
      );
      const links = targets.filter((t) => existsSync(join(vault, t)));
      return {
        path,
        title: get("title") || path.split("/").pop()!.replace(/\.md$/, ""),
        type: get("type"),
        status: get("status"),
        description: get("description"),
        aliases: [],
        links,
        broken: targets.length - links.length,
      };
    });
}
