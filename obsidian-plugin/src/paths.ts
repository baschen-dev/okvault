// Where okvault lives in the Obsidian vault, and which of its files it ignores.
// Pure: no Obsidian import, so it is tested under Node. The matcher mirrors
// internal/vault/ignore.go; both run the cases in internal/vault/testdata.

export const IGNORE_FILE = ".vaultignore";

/**
 * The okvault root as a folder path relative to the Obsidian vault, "" for the
 * vault itself. Accepts ".", "/", "./docs/", "docs\\kb" and the like; returns
 * null for a path that leaves the vault ("..").
 */
export function normalizeRoot(input: string): string | null {
  const segs = (input ?? "")
    .trim()
    .replace(/\\/g, "/")
    .split("/")
    .filter((s) => s !== "" && s !== ".");
  if (segs.some((s) => s === "..")) return null;
  return segs.join("/");
}

interface Rule {
  re: RegExp;
  negate: boolean;
  dirOnly: boolean;
}

/** A parsed .vaultignore: gitignore-style rules, the last match wins. */
export class VaultIgnore {
  private rules: Rule[] = [];

  constructor(src = "") {
    for (let line of src.replace(/\r\n/g, "\n").split("\n")) {
      line = line.replace(/[ \t]+$/, "");
      if (!line || line.startsWith("#")) continue;
      const negate = line.startsWith("!");
      if (negate) line = line.slice(1);
      line = line.replace(/\\/g, "/");
      const dirOnly = line.endsWith("/");
      if (dirOnly) line = line.replace(/\/+$/, "");
      const anchored = line.includes("/");
      line = line.replace(/^\/+/, "");
      if (!line) continue;
      this.rules.push({ re: new RegExp((anchored ? "^" : "^(?:.*/)?") + globToRegExp(line) + "$"), negate, dirOnly });
    }
  }

  get empty(): boolean {
    return this.rules.length === 0;
  }

  private matchSelf(rel: string, dir: boolean): boolean {
    let ignored = false;
    for (const r of this.rules) {
      if (r.dirOnly && !dir) continue;
      if (r.re.test(rel)) ignored = !r.negate;
    }
    return ignored;
  }

  /** Whether an okvault path is ignored, itself or through a parent folder. */
  match(rel: string, dir = false): boolean {
    if (!this.rules.length) return false;
    rel = rel.replace(/\\/g, "/").replace(/^\/+|\/+$/g, "");
    if (!rel || rel === ".") return false;
    const segs = rel.split("/");
    for (let i = 1; i < segs.length; i++) {
      if (this.matchSelf(segs.slice(0, i).join("/"), true)) return true;
    }
    return this.matchSelf(rel, dir);
  }
}

function globToRegExp(g: string): string {
  let out = "";
  for (let i = 0; i < g.length; i++) {
    const rest = g.slice(i);
    if (rest.startsWith("**/")) {
      out += "(?:.*/)?";
      i += 2;
    } else if (rest === "/**") {
      out += "/.*";
      i += 2;
    } else if (rest.startsWith("**")) {
      out += ".*";
      i++;
    } else if (g[i] === "*") out += "[^/]*";
    else if (g[i] === "?") out += "[^/]";
    else out += g[i].replace(/[.+^${}()|[\]\\/-]/g, "\\$&");
  }
  return out;
}
