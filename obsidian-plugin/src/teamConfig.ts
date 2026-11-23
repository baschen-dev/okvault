// Reads and edits the vault's team settings (.okvault.yaml) while keeping the
// file's comments, key order and untouched keys. Field checks mirror
// internal/config/config.go for instant hints; okvault itself (`okvault lint`)
// is the authority before anything is written. No Obsidian import: tested under Node.
import { isMap, parseDocument, type YAMLMap } from "yaml";

export const CONFIG_FILE = ".okvault.yaml";

export type GitMode = "off" | "commit" | "push" | "pr";
export type TrackerKind = "" | "none" | "jira";

export interface OwnerRow {
  folder: string;
  owners: string[];
}

export interface TeamConfig {
  git: {
    mode: GitMode;
    remote: string;
    base: string;
    branch: string;
    pull_interval: string;
    commit_edits: string;
  };
  review_interval: string;
  tracker: {
    kind: TrackerKind;
    url: string;
    projects: string[];
    api: "" | "2" | "3";
    browse: string;
  };
  owners: OwnerRow[];
}

export function emptyConfig(): TeamConfig {
  return {
    git: { mode: "off", remote: "", base: "", branch: "", pull_interval: "", commit_edits: "" },
    review_interval: "",
    tracker: { kind: "", url: "", projects: [], api: "", browse: "" },
    owners: [],
  };
}

const str = (v: unknown): string => (v === null || v === undefined ? "" : String(v).trim());
const list = (v: unknown): string[] =>
  Array.isArray(v) ? v.map(str).filter(Boolean) : typeof v === "string" ? splitList(v) : [];

/** "a, b c" -> ["a", "b", "c"] */
export function splitList(s: string): string[] {
  return s.split(/[,\s]+/).map((x) => x.trim()).filter(Boolean);
}

/** Parses .okvault.yaml into the editor's model. A YAML syntax error is returned, not thrown. */
export function readTeamConfig(src: string): { config: TeamConfig; error?: string } {
  const c = emptyConfig();
  const doc = parseDocument(src);
  if (doc.errors.length) return { config: c, error: doc.errors[0].message };
  const js = (doc.toJS() ?? {}) as Record<string, any>;
  if (typeof js !== "object" || Array.isArray(js)) return { config: c, error: "the file is not a YAML map" };
  const g = js.git ?? {};
  const mode = str(g.mode).toLowerCase();
  c.git = {
    mode: (["commit", "push", "pr"].includes(mode) ? mode : "off") as GitMode,
    remote: str(g.remote),
    base: str(g.base),
    branch: str(g.branch),
    pull_interval: str(g.pull_interval),
    commit_edits: str(g.commit_edits),
  };
  c.review_interval = str(js.review_interval);
  const t = js.tracker ?? {};
  const kind = str(t.kind).toLowerCase();
  c.tracker = {
    kind: kind === "jira" || kind === "none" ? kind : "",
    url: str(t.url),
    projects: list(t.projects),
    api: str(t.api) === "2" || str(t.api) === "3" ? (str(t.api) as "2" | "3") : "",
    browse: str(t.browse),
  };
  if (js.owners && typeof js.owners === "object") {
    for (const [folder, who] of Object.entries(js.owners)) c.owners.push({ folder, owners: list(who) });
  }
  return { config: c };
}

/**
 * Writes the model into the existing file text: changed keys are set, emptied
 * keys removed, everything else (comments, order, other keys) left as it was.
 */
export function applyTeamConfig(src: string, c: TeamConfig): string {
  const doc = parseDocument(src);
  if (doc.errors.length) throw new Error(`cannot edit ${CONFIG_FILE}: ${doc.errors[0].message}`);
  // Nothing changed: leave the file byte for byte as it is.
  if (same(normalized(readTeamConfig(src).config), normalized(c))) return src;
  if (!isMap(doc.contents)) {
    // Only comments (or empty): keep them above the new settings.
    const comment = doc.commentBefore ?? doc.comment ?? (doc.contents as { commentBefore?: string } | null)?.commentBefore;
    (doc as { contents: unknown }).contents = doc.createNode({});
    if (comment) doc.commentBefore = comment;
    doc.comment = null;
  }

  const set = (path: string[], v: string) => {
    if (v) {
      if (str(doc.getIn(path)) !== v) doc.setIn(path, v); // equal values keep their quoting and comments
    } else if (doc.hasIn(path)) doc.deleteIn(path);
  };
  set(["git", "mode"], c.git.mode === "off" ? "" : c.git.mode);
  set(["git", "remote"], c.git.remote === "origin" ? "" : c.git.remote);
  set(["git", "base"], c.git.base);
  set(["git", "branch"], c.git.branch);
  set(["git", "pull_interval"], c.git.pull_interval);
  set(["git", "commit_edits"], c.git.commit_edits);
  set(["review_interval"], c.review_interval);

  set(["tracker", "kind"], c.tracker.kind);
  set(["tracker", "url"], c.tracker.url);
  const projects = c.tracker.projects.map((p) => p.toUpperCase());
  if (projects.length && same(list(toJS(doc.getIn(["tracker", "projects"]))), projects)) {
    // unchanged
  } else if (projects.length) {
    const node = doc.createNode(c.tracker.projects.map((p) => p.toUpperCase()));
    (node as { flow?: boolean }).flow = true;
    doc.setIn(["tracker", "projects"], node);
  } else if (doc.hasIn(["tracker", "projects"])) doc.deleteIn(["tracker", "projects"]);
  set(["tracker", "api"], c.tracker.api);
  set(["tracker", "browse"], c.tracker.browse);

  // Owners: drop folders no longer listed, then set each row (existing ones keep their place).
  const rows = c.owners.filter((r) => r.folder.trim() && r.owners.length);
  const keep = new Set(rows.map((r) => normFolder(r.folder)));
  const existing = doc.getIn(["owners"]);
  if (isMap(existing)) {
    for (const item of [...existing.items]) {
      const k = str((item.key as { value?: unknown })?.value ?? item.key);
      if (!keep.has(normFolder(k))) existing.delete(item.key);
    }
  }
  for (const r of rows) {
    const current = isMap(existing) ? findKey(existing, r.folder) : undefined;
    if (current !== undefined && same(list(toJS(existing && isMap(existing) ? existing.get(current) : null)), r.owners)) continue;
    const node = doc.createNode(r.owners);
    (node as { flow?: boolean }).flow = true;
    const key = isMap(existing) ? findKey(existing, r.folder) ?? normFolder(r.folder) : normFolder(r.folder);
    doc.setIn(["owners", key], node);
  }

  for (const k of ["git", "tracker", "owners"]) {
    const m = doc.getIn([k]);
    if (isMap(m) && m.items.length === 0) doc.deleteIn([k]);
  }
  if (isMap(doc.contents) && doc.contents.items.length === 0) {
    // Every setting removed: keep the file's comments, without a stray "{}".
    const c = [doc.commentBefore, doc.contents.commentBefore, doc.comment].filter(Boolean).join("\n");
    return c ? c.split("\n").map((l) => (l === "" ? "" : l.trim() === "" ? "#" : `#${l}`)).join("\n") + "\n" : "";
  }
  return doc.toString({ lineWidth: 0, flowCollectionPadding: false });
}

function toJS(v: unknown): unknown {
  return v && typeof v === "object" && "toJSON" in v ? (v as { toJSON(): unknown }).toJSON() : v;
}

function same(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

/** The model as okvault would read it, so cosmetic differences do not count as changes. */
function normalized(c: TeamConfig): TeamConfig {
  const n = structuredClone(c);
  n.tracker.projects = n.tracker.projects.map((p) => p.toUpperCase());
  if (n.git.remote === "origin") n.git.remote = "";
  n.owners = n.owners
    .filter((r) => r.folder.trim() && r.owners.length)
    .map((r) => ({ folder: normFolder(r.folder), owners: r.owners }));
  return n;
}

function normFolder(f: string): string {
  const d = f.trim().replace(/\\/g, "/").replace(/^\/+|\/+$/g, "");
  return d === "" ? "." : d;
}

function findKey(m: YAMLMap, folder: string): string | undefined {
  for (const item of m.items) {
    const k = str((item.key as { value?: unknown })?.value ?? item.key);
    if (normFolder(k) === normFolder(folder)) return k;
  }
  return undefined;
}

// ---- checks, mirroring internal/config/config.go ----

const gitName = /^[A-Za-z0-9][A-Za-z0-9._/-]*$/;
const projectKey = /^[A-Z][A-Z0-9_]{0,19}$/;
const goDuration = /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/;

/** okvault's ParseInterval: "30d", "4w" or a Go duration ("720h", "10m"), > 0. */
export function intervalError(s: string, allowZero = false): string | null {
  const v = s.trim();
  if (!v || (allowZero && v === "0")) return null;
  const dw = /^(\d+)([dw])$/.exec(v);
  if (dw) return Number(dw[1]) > 0 ? null : `invalid interval "${v}"`;
  if (goDuration.test(v) && /[1-9]/.test(v)) return null;
  return `invalid interval "${v}" (use 180d, 26w, 10m or 4320h)`;
}

function gitNameError(field: string, v: string): string | null {
  if (!v) return null;
  if (!gitName.test(v) || v.includes("..") || v.endsWith("/")) return `${field} "${v}" is not a valid git name`;
  return null;
}

/** Field-level problems, keyed by field ("git.base", "tracker.url", "owners.2", ...). */
export function checkTeamConfig(c: TeamConfig): Map<string, string> {
  const out = new Map<string, string>();
  const add = (k: string, e: string | null) => e && out.set(k, e);
  add("git.remote", gitNameError("git.remote", c.git.remote) ?? (c.git.remote.includes("/") ? `git.remote "${c.git.remote}" is a remote name like origin, not a URL` : null));
  add("git.base", gitNameError("git.base", c.git.base));
  add("git.branch", gitNameError("git.branch", c.git.branch));
  add("git.pull_interval", intervalError(c.git.pull_interval, true));
  add("git.commit_edits", intervalError(c.git.commit_edits, true));
  add("review_interval", intervalError(c.review_interval));
  const t = c.tracker;
  if (t.url) {
    let u: URL | null = null;
    try {
      u = new URL(t.url);
    } catch {
      /* reported below */
    }
    if (!u || (u.protocol !== "https:" && u.protocol !== "http:") || !u.host) add("tracker.url", `tracker.url "${t.url}" is not an http(s) URL`);
    else if (u.username || u.password) add("tracker.url", "tracker.url must not contain credentials; use JIRA_EMAIL / JIRA_API_TOKEN");
  } else if (t.kind === "jira") add("tracker.url", "tracker.url is required for Jira");
  const bad = t.projects.map((p) => p.toUpperCase()).filter((p) => !projectKey.test(p));
  if (bad.length) add("tracker.projects", `not project keys: ${bad.join(", ")} (e.g. PROJ)`);
  if (t.browse && !t.browse.includes("{key}")) add("tracker.browse", "the browse URL needs {key} where the ticket key goes");
  const seen = new Set<string>();
  c.owners.forEach((r, i) => {
    if (!r.folder.trim() && !r.owners.length) return;
    if (!r.owners.length) add(`owners.${i}`, "add at least one owner, or remove the row");
    const f = normFolder(r.folder);
    if (seen.has(f)) add(`owners.${i}`, `"${f}" is listed twice`);
    seen.add(f);
  });
  return out;
}
