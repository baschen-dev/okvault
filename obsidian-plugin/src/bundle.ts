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

export function buildBundle(
  notes: NoteInput[],
  reasons: Map<string, string[]>,
  radius = 300,
): Bundle {
  const concepts = notes.filter((n) => isConceptPath(n.path));
  const bySection = new Map<string, NoteInput[]>();
  for (const n of concepts) {
    const s = sectionOf(n.path);
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
      const rs = new Set(reasons.get(n.path) ?? []);
      if (n.broken > 0) rs.add("broken-links");
      if ((n.status ?? "").toLowerCase() === "draft") rs.add("draft");
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
