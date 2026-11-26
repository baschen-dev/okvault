// The OKF bundle as a graph: sections (top-level folders, each with its index.md),
// concepts (typed notes) and the links between them, laid out on a circle with
// links bundled through their sections. Pure: no Obsidian import, so it is tested
// under Node.

export interface NoteInput {
  /** okvault (bundle-relative) path, e.g. faq/how-do-i-add-an-faq.md */
  path: string;
  title: string;
  type: string; // "" for an untyped note
  status?: string; // draft | stable | deprecated
  /** okvault paths this note links to (resolved) */
  links: string[];
  /** number of links that do not resolve */
  broken: number;
}

export interface Node {
  path: string;
  title: string;
  type: string;
  section: string;
  status: string;
  angle: number; // radians, 0 = top, clockwise
  x: number;
  y: number;
  degree: number;
  reasons: Set<string>;
}

export interface Edge {
  from: string;
  to: string;
  intra: boolean; // both ends in the same section
  d: string; // SVG path
}

export interface Section {
  name: string;
  start: number;
  end: number;
  anchor: { x: number; y: number };
  count: number;
  types: Map<string, number>;
  internal: number; // links inside the section
  external: number; // links leaving it
  review: number; // notes in it with a review reason
}

export interface Bundle {
  nodes: Map<string, Node>;
  edges: Edge[];
  sections: Section[];
  types: string[]; // by frequency, then name
  radius: number;
}

export const ROOT = "(root)";
export const UNTYPED = "Untyped";

/** Paths okvault does not treat as concepts. */
export function isConceptPath(p: string): boolean {
  if (!p.endsWith(".md")) return false;
  const base = p.split("/").pop()!;
  if (base === "index.md" || base === "log.md") return false;
  return !p.split("/").some((seg) => seg.startsWith("_") || seg.startsWith("."));
}

export function sectionOf(p: string): string {
  const i = p.indexOf("/");
  return i < 0 ? ROOT : p.slice(0, i);
}

/** Whether a note lies in a folder ("" = the whole vault). */
export function inFolder(p: string, folder: string): boolean {
  return !folder || p.startsWith(folder + "/");
}

/** The group a note is drawn in when the map shows a folder: its subfolder, or ROOT for the folder's own notes. */
export function groupOf(p: string, folder: string): string {
  return sectionOf(folder ? p.slice(folder.length + 1) : p);
}

/**
 * Why a note needs review: okvault's kb_review reasons for it (stale, unverified,
 * no-owner, ...) plus what is known locally (broken links, draft status).
 */
export function reviewReasons(n: NoteInput, reasons: Map<string, string[]>): Set<string> {
  const rs = new Set(reasons.get(n.path) ?? []);
  if (n.broken > 0) rs.add("broken-links");
  if ((n.status ?? "").toLowerCase() === "draft") rs.add("draft");
  return rs;
}

/**
 * What the bundle map shows: notes of hidden types or in hidden sections are left
 * out, and when onlyReasons is not empty only notes with at least one of those
 * review reasons stay.
 */
export interface GraphFilter {
  hiddenTypes: Set<string>;
  hiddenSections: Set<string>;
  onlyReasons: Set<string>;
}

/** The concepts a filter keeps. Links to dropped notes vanish with them in buildBundle. */
export function filterNotes<T extends NoteInput>(
  notes: T[],
  f: GraphFilter,
  reasons: Map<string, string[]> = new Map(),
): T[] {
  return notes.filter((n) => {
    if (!isConceptPath(n.path) || f.hiddenTypes.has(n.type || UNTYPED) || f.hiddenSections.has(sectionOf(n.path))) return false;
    if (!f.onlyReasons.size) return true;
    for (const r of reviewReasons(n, reasons)) if (f.onlyReasons.has(r)) return true;
    return false;
  });
}

/** Every type, section and review reason in the vault, filtered or not: the filter controls and the colours come from it. */
export interface Catalog {
  types: string[]; // by frequency, then name (as Bundle.types)
  typeCount: Map<string, number>;
  sections: { name: string; count: number }[]; // in map order
  reasonCount: Map<string, number>; // notes with each review reason
  total: number;
}

export function catalog(notes: NoteInput[], reasons: Map<string, string[]> = new Map()): Catalog {
  const typeCount = new Map<string, number>();
  const secCount = new Map<string, number>();
  const reasonCount = new Map<string, number>();
  let total = 0;
  for (const n of notes) {
    if (!isConceptPath(n.path)) continue;
    total++;
    const t = n.type || UNTYPED;
    typeCount.set(t, (typeCount.get(t) ?? 0) + 1);
    const s = sectionOf(n.path);
    secCount.set(s, (secCount.get(s) ?? 0) + 1);
    for (const r of reviewReasons(n, reasons)) reasonCount.set(r, (reasonCount.get(r) ?? 0) + 1);
  }
  const types = [...typeCount.entries()].sort((x, y) => y[1] - x[1] || x[0].localeCompare(y[0])).map(([t]) => t);
  const sections = [...secCount.entries()]
    .sort(([a], [b]) => (a === ROOT ? -1 : b === ROOT ? 1 : a.localeCompare(b)))
    .map(([name, count]) => ({ name, count }));
  return { types, typeCount, sections, reasonCount, total };
}

/**
 * Lays out the concepts of a folder ("" = the whole vault): its subfolders are the
 * sections around the circle, and links leaving the folder are left out.
 */
export function buildBundle(
  notes: NoteInput[],
  reasons: Map<string, string[]>,
  radius = 300,
  folder = "",
): Bundle {
  const concepts = notes.filter((n) => isConceptPath(n.path) && inFolder(n.path, folder));
  const bySection = new Map<string, NoteInput[]>();
  for (const n of concepts) {
    const s = groupOf(n.path, folder);
    if (!bySection.has(s)) bySection.set(s, []);
    bySection.get(s)!.push(n);
  }
  const names = [...bySection.keys()].sort((a, b) =>
    a === ROOT ? -1 : b === ROOT ? 1 : a.localeCompare(b),
  );

  // Even spacing with a gap (worth 1.5 notes, at least a little) between sections.
  const gap = names.length > 1 ? 1.5 : 0;
  const slots = concepts.length + gap * names.length || 1;
  const step = (2 * Math.PI) / slots;
  const nodes = new Map<string, Node>();
  const sections: Section[] = [];
  let a = (gap * step) / 2;
  const typeCount = new Map<string, number>();

  for (const name of names) {
    const members = bySection.get(name)!;
    // Within a section: by type, then title, so types cluster.
    members.sort(
      (x, y) => (x.type || UNTYPED).localeCompare(y.type || UNTYPED) || x.title.localeCompare(y.title),
    );
    const start = a;
    const types = new Map<string, number>();
    let review = 0;
    for (const n of members) {
      const angle = a + step / 2;
      const type = n.type || UNTYPED;
      types.set(type, (types.get(type) ?? 0) + 1);
      typeCount.set(type, (typeCount.get(type) ?? 0) + 1);
      const rs = reviewReasons(n, reasons);
      if (rs.size) review++;
      nodes.set(n.path, {
        path: n.path,
        title: n.title,
        type,
        section: name,
        status: n.status ?? "",
        angle,
        ...polar(angle, radius),
        degree: 0,
        reasons: rs,
      });
      a += step;
    }
    const end = a;
    const mid = (start + end) / 2;
    sections.push({
      name,
      start,
      end,
      anchor: polar(mid, radius * 0.42),
      count: members.length,
      types,
      internal: 0,
      external: 0,
      review,
    });
    a += gap * step;
  }

  const secByName = new Map(sections.map((s) => [s.name, s]));
  const edges: Edge[] = [];
  const seen = new Set<string>();
  for (const n of concepts) {
    for (const to of n.links) {
      if (to === n.path || !nodes.has(to)) continue;
      const key = n.path < to ? `${n.path}\n${to}` : `${to}\n${n.path}`;
      const src = nodes.get(n.path)!;
      const dst = nodes.get(to)!;
      src.degree++;
      dst.degree++;
      if (seen.has(key)) continue; // draw a mutual link once
      seen.add(key);
      const intra = src.section === dst.section;
      if (intra) secByName.get(src.section)!.internal++;
      else {
        secByName.get(src.section)!.external++;
        secByName.get(dst.section)!.external++;
      }
      edges.push({ from: n.path, to, intra, d: edgePath(src, dst, secByName, radius, intra) });
    }
  }

  const types = [...typeCount.entries()]
    .sort((x, y) => y[1] - x[1] || x[0].localeCompare(y[0]))
    .map(([t]) => t);
  return { nodes, edges, sections, types, radius };
}

/** A link bends through its sections' anchors, so links between two sections bundle together. */
function edgePath(
  s: Node,
  t: Node,
  secs: Map<string, Section>,
  r: number,
  intra: boolean,
): string {
  if (intra) {
    const c1 = polar(s.angle, r * 0.72);
    const c2 = polar(t.angle, r * 0.72);
    return `M${f(s.x)},${f(s.y)} C${f(c1.x)},${f(c1.y)} ${f(c2.x)},${f(c2.y)} ${f(t.x)},${f(t.y)}`;
  }
  const a1 = secs.get(s.section)!.anchor;
  const a2 = secs.get(t.section)!.anchor;
  return `M${f(s.x)},${f(s.y)} C${f(a1.x)},${f(a1.y)} ${f(a2.x)},${f(a2.y)} ${f(t.x)},${f(t.y)}`;
}

/** How the map is drawn for a given on-screen size: label font and length, and the viewBox that fits them. */
export interface LabelGeometry {
  font: number; // label font size, in viewBox units
  maxChars: number; // 0 = titles on hover only
  half: number; // the viewBox runs from -half to +half both ways
}

const CHAR_W = 0.56; // average glyph width, in ems
const LABEL_GAP = 14; // between the section arcs and the labels
const ARC_OUT = 26; // outer edge of the section arcs, past the ring

/**
 * Sizes the labels so they read at `px` screen pixels on a chart `screen` pixels wide,
 * while the ring keeps at least half of the chart: a narrow pane gets shorter titles,
 * and below 14 characters titles move to hover only.
 */
export function labelGeometry(radius: number, screen: number, px = 11, cap = 32): LabelGeometry {
  const inner = radius + ARC_OUT + LABEL_GAP;
  // Labels of m characters take m·CHAR_W·font on each side; the ring keeps half the width.
  const maxChars = Math.min(cap, Math.floor(screen / (4 * CHAR_W * px)));
  if (maxChars < 14) return { font: (px * (radius + ARC_OUT + 10)) / (screen / 2), maxChars: 0, half: radius + ARC_OUT + 10 };
  const half = inner / (1 - (2 * CHAR_W * px * maxChars) / screen);
  return { font: (px * 2 * half) / screen, maxChars, half };
}

/** The radius labels start at. */
export function labelRadius(radius: number): number {
  return radius + ARC_OUT + LABEL_GAP;
}

/** A title cut to n characters at a word boundary when one is close, with an ellipsis. */
export function shortTitle(s: string, n: number): string {
  s = s.trim();
  if (s.length <= n) return s;
  const cut = s.slice(0, n - 1);
  const sp = cut.lastIndexOf(" ");
  const head = sp >= n * 0.6 ? cut.slice(0, sp) : cut;
  return head.replace(/[\s,.;:–—-]+$/, "") + "…";
}

/** Review reasons, most urgent first (as the map's rings rank them). */
const URGENCY = ["broken-links", "stale", "unverified", "draft", "no-owner"];

/**
 * Which notes get a title on the ring: every note when they all fit one line-height
 * apart; otherwise the ones needing review (most urgent first), then the most linked,
 * each kept only if no chosen title is closer than a line-height.
 */
export function pickLabels(nodes: Node[], radius: number, font: number): Set<string> {
  const minGap = (font * 1.15) / labelRadius(radius); // radians between two labels
  const sorted = [...nodes].sort((a, b) => a.angle - b.angle);
  const fitsAll = sorted.every((n, i) => {
    const next = sorted[(i + 1) % sorted.length];
    const d = i + 1 < sorted.length ? next.angle - n.angle : next.angle + 2 * Math.PI - n.angle;
    return sorted.length < 2 || d >= minGap - 1e-9;
  });
  if (fitsAll) return new Set(nodes.map((n) => n.path));
  const rank = (n: Node) => {
    const i = URGENCY.findIndex((r) => n.reasons.has(r));
    return i < 0 ? URGENCY.length : i;
  };
  const order = [...nodes].sort((a, b) => rank(a) - rank(b) || b.degree - a.degree || a.title.localeCompare(b.title));
  const taken: number[] = []; // sorted angles
  const out = new Set<string>();
  const near = (a: number) => {
    // binary search for the insertion point, then check both neighbours (and wrap-around)
    let lo = 0;
    let hi = taken.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (taken[mid] < a) lo = mid + 1;
      else hi = mid;
    }
    const gap = (x: number) => {
      const d = Math.abs(x - a) % (2 * Math.PI);
      return Math.min(d, 2 * Math.PI - d);
    };
    const cands = [taken[lo], taken[lo - 1], taken[0], taken[taken.length - 1]];
    return cands.some((x) => x !== undefined && gap(x) < minGap);
  };
  for (const n of order) {
    if (near(n.angle)) continue;
    let lo = 0;
    while (lo < taken.length && taken[lo] < n.angle) lo++;
    taken.splice(lo, 0, n.angle);
    out.add(n.path);
  }
  return out;
}

export function polar(angle: number, r: number): { x: number; y: number } {
  return { x: r * Math.sin(angle), y: -r * Math.cos(angle) };
}

/** SVG path for a ring segment between two angles. */
export function arcPath(a0: number, a1: number, r0: number, r1: number): string {
  const p0 = polar(a0, r1);
  const p1 = polar(a1, r1);
  const p2 = polar(a1, r0);
  const p3 = polar(a0, r0);
  const large = a1 - a0 > Math.PI ? 1 : 0;
  return (
    `M${f(p0.x)},${f(p0.y)} A${r1},${r1} 0 ${large} 1 ${f(p1.x)},${f(p1.y)} ` +
    `L${f(p2.x)},${f(p2.y)} A${r0},${r0} 0 ${large} 0 ${f(p3.x)},${f(p3.y)} Z`
  );
}

function f(n: number): string {
  return n.toFixed(1);
}
