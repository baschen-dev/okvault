// What a note's frontmatter says about trust (who wrote it, who checked it, when it
// goes stale), plus small helpers for tickets and file names. Mirrors
// internal/okf/trust.go and internal/vault/team.go. No Obsidian import: tested under Node.

export interface Stamp {
  by: string;
  at: Date;
}

export interface Trust {
  generated?: Stamp; // written by an agent (okvault stamps generated: on kb_write)
  verified: Stamp[]; // people who checked it, oldest first
  lastVerified?: Stamp;
  staleAfter?: Date;
  staleAfterInvalid: boolean;
  stale: boolean; // stale_after has passed
  daysLeft?: number; // until stale_after (negative: days since)
  unverified: boolean; // agent edit newer than the last person's check
}

function toDate(v: unknown): Date | undefined {
  if (v instanceof Date) return Number.isNaN(v.getTime()) ? undefined : v;
  if (typeof v === "string" || typeof v === "number") {
    const d = new Date(v);
    return Number.isNaN(d.getTime()) ? undefined : d;
  }
  return undefined;
}

function stamp(v: unknown): Stamp | undefined {
  if (!v || typeof v !== "object" || Array.isArray(v)) return undefined;
  const m = v as Record<string, unknown>;
  const at = toDate(m.at);
  const by = typeof m.by === "string" ? m.by : "";
  return by && at ? { by, at } : undefined;
}

export function trustOf(fm: Record<string, unknown> | undefined, now = new Date()): Trust {
  const f = fm ?? {};
  const generated = stamp(f.generated);
  const raw = f.verified;
  const verified = (Array.isArray(raw) ? raw.map(stamp) : [stamp(raw)])
    .filter((s): s is Stamp => !!s)
    .sort((a, b) => a.at.getTime() - b.at.getTime());
  const lastVerified = verified[verified.length - 1];
  const present = f.stale_after !== undefined && f.stale_after !== null && f.stale_after !== "";
  const staleAfter = present ? toDate(f.stale_after) : undefined;
  const t: Trust = {
    generated,
    verified,
    lastVerified,
    staleAfter,
    staleAfterInvalid: present && !staleAfter,
    stale: !!staleAfter && now > staleAfter,
    unverified: !!generated && (!lastVerified || lastVerified.at < generated.at),
  };
  if (staleAfter) t.daysLeft = Math.round((staleAfter.getTime() - now.getTime()) / 86_400_000);
  return t;
}

/** A ticket key like PROJ-123 (okvault's NormalizeKey shape). */
export const TICKET_KEY = /\b[A-Z][A-Z0-9_]{0,19}-[1-9][0-9]*\b/g;

/** The ticket key under (or touching) a cursor column, if any. Only configured projects count when given. */
export function ticketKeyAt(line: string, ch: number, projects: string[] = []): string | null {
  for (const m of line.matchAll(TICKET_KEY)) {
    const start = m.index ?? 0;
    const end = start + m[0].length;
    if (ch >= start && ch <= end && isProjectKey(m[0], projects)) return m[0];
  }
  return null;
}

export function isProjectKey(key: string, projects: string[]): boolean {
  if (!projects.length) return true;
  const p = key.slice(0, key.lastIndexOf("-"));
  return projects.some((x) => x.toUpperCase() === p);
}

/** Normalises a typed key ("proj-12 " -> "PROJ-12"), or null when it is not one. */
export function normalizeKey(s: string): string | null {
  const k = s.trim().toUpperCase();
  return /^[A-Z][A-Z0-9_]{0,19}-[1-9][0-9]*$/.test(k) ? k : null;
}

/** "How do I Release?" -> "how-do-i-release" (okvault's file name convention). */
export function kebab(s: string): string {
  return s
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 80)
    .replace(/-+$/, "");
}

/** Where notes of a type usually live: the most common folder among them ("" = vault root). */
export function folderFor(type: string, notes: { path: string; type: string }[]): string {
  const counts = new Map<string, number>();
  for (const n of notes) {
    if (n.type.toLowerCase() !== type.toLowerCase()) continue;
    if (n.path.split("/").some((seg) => seg.startsWith("_") || seg.startsWith("."))) continue; // templates, config
    const d = n.path.includes("/") ? n.path.slice(0, n.path.lastIndexOf("/")) : "";
    counts.set(d, (counts.get(d) ?? 0) + 1);
  }
  let best = "";
  let n = 0;
  for (const [d, c] of counts) if (c > n || (c === n && d < best)) [best, n] = [d, c];
  return n ? best : kebab(type) === "faq" ? "faq" : "";
}

/** Tickets a note documents, from its frontmatter (a list or a comma-separated string). */
export function ticketsOf(fm: Record<string, unknown> | undefined): string[] {
  const v = fm?.tickets;
  const raw = Array.isArray(v) ? v : typeof v === "string" ? v.split(/[,\s]+/) : [];
  return raw.map((x) => normalizeKey(String(x))).filter((k): k is string => !!k);
}

export function shortDate(d: Date): string {
  return d.toISOString().slice(0, 10);
}

/**
 * Fills a _templates note: {{title}} is escaped inside the YAML frontmatter and
 * kept as typed in the body. The FAQ template adds the question mark itself.
 */
export function fillTemplate(text: string, type: string, title: string): string {
  const q = type.toLowerCase() === "faq" ? title.replace(/\?+$/, "") : title;
  const end = text.startsWith("---") ? text.indexOf("\n---", 3) : -1;
  const yamlPart = end > 0 ? text.slice(0, end) : "";
  const yamlSafe = q.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
  return yamlPart.replace(/\{\{title\}\}/g, () => yamlSafe) + text.slice(yamlPart.length).replace(/\{\{title\}\}/g, () => q);
}

/** folder + kebab-case title + .md, or null when the title has no usable characters. */
export function notePath(folder: string, title: string): string | null {
  const name = kebab(title);
  if (!name) return null;
  const f = folder.trim().replace(/^\/+|\/+$/g, "");
  return f ? `${f}/${name}.md` : `${name}.md`;
}

/** A relative Markdown link from one vault path to another (OKF style, no wikilinks). */
export function relativeLink(from: string, to: string): string {
  const a = from.split("/").slice(0, -1);
  const b = to.split("/");
  let i = 0;
  while (i < a.length && i < b.length - 1 && a[i] === b[i]) i++;
  const up = a.length - i;
  const rel = [...Array<string>(up).fill(".."), ...b.slice(i)].join("/");
  return up ? rel : `./${rel}`;
}

/** Existing aliases plus new phrasings, without duplicates of each other or of the title (case, punctuation ignored). */
export function mergeAliases(existing: unknown, add: string[], title: string): string[] {
  const norm = (s: string) => s.toLowerCase().replace(/[^\p{L}\p{N}]+/gu, " ").trim();
  const cur = Array.isArray(existing) ? existing.map(String) : typeof existing === "string" && existing ? [existing] : [];
  const seen = new Set([norm(title), ...cur.map(norm)]);
  const out = [...cur];
  for (const p of add) {
    const n = norm(p);
    if (n && !seen.has(n)) {
      seen.add(n);
      out.push(p);
    }
  }
  return out;
}
