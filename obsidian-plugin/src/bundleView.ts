import { ItemView, WorkspaceLeaf, debounce, setIcon } from "obsidian";
import { Bundle, Catalog, GraphFilter, Node, arcPath, buildBundle, catalog, filterNotes, polar, ROOT, UNTYPED } from "./bundle";
import type OkvaultPlugin from "./main";

export const BUNDLE_VIEW = "okvault-bundle";

const RADIUS = 260;
const MAX_SLOTS = 7; // categorical slots; further types fold into "Other"
const LABEL_LIMIT = 120; // above this many notes, titles show on hover only

/** Review reasons, most urgent first, as okvault's kb_review names them. */
const REASONS: { key: string; label: string; glyph: string; cls: string }[] = [
  { key: "broken-links", label: "Broken links", glyph: "⛔", cls: "critical" },
  { key: "stale", label: "Stale", glyph: "⏰", cls: "serious" },
  { key: "unverified", label: "Agent edit, not verified", glyph: "⚠", cls: "warning" },
  { key: "draft", label: "Draft", glyph: "✎", cls: "muted" },
  { key: "no-owner", label: "No owner", glyph: "👤", cls: "muted" },
];

/** A map of the OKF bundle: sections around a circle, notes coloured by type, links bundled through sections. */
export class BundleView extends ItemView {
  private bundle: Bundle | null = null;
  private reasons = new Map<string, string[]>();
  private reviewSource: "server" | "local" = "local";
  private catalog: Catalog = { types: [], typeCount: new Map(), sections: [], total: 0 };
  private focusSection: string | null = null;
  private svg!: SVGSVGElement;
  private tip!: HTMLElement;
  private legendEl!: HTMLElement;
  private infoEl!: HTMLElement;
  private tableEl!: HTMLElement;
  private slot = new Map<string, string>(); // type -> css class

  private rerender = debounce(() => this.render(), 400, true);

  constructor(
    leaf: WorkspaceLeaf,
    private plugin: OkvaultPlugin,
  ) {
    super(leaf);
  }

  getViewType(): string {
    return BUNDLE_VIEW;
  }
  getDisplayText(): string {
    return "okvault bundle";
  }
  override getIcon(): string {
    return "orbit";
  }

  override async onOpen(): Promise<void> {
    const root = this.contentEl;
    root.empty();
    root.addClass("okvault-bundle");

    const bar = root.createDiv({ cls: "okvault-toolbar" });
    this.infoEl = bar.createDiv({ cls: "okvault-info" });
    const btn = bar.createEl("button", { cls: "clickable-icon", attr: { "aria-label": "Rescan the vault (okvault re-reads every note: broken links, review status, .vaultignore)" } });
    setIcon(btn, "refresh-cw");
    btn.addEventListener("click", async () => {
      btn.disabled = true;
      try {
        // Offline, rescan does nothing: still redraw from Obsidian's own cache.
        if (!(await this.plugin.rescan())) {
          await this.plugin.loadIgnore();
          await this.reload();
        }
      } finally {
        btn.disabled = false;
      }
    });

    this.legendEl = root.createDiv({ cls: "okvault-legend" });
    const wrap = root.createDiv({ cls: "okvault-chart" });
    this.svg = wrap.createSvg("svg", {
      attr: { viewBox: "-470 -400 940 800", role: "img", "aria-label": "Bundle map: notes grouped by section, coloured by type, with their links" },
    });
    this.tip = wrap.createDiv({ cls: "okvault-tip" });
    this.tip.hide();
    this.svg.addEventListener("click", (e) => {
      if (e.target === this.svg) this.setFocus(null);
    });

    root.createDiv({ cls: "okvault-section-title", text: "Sections" });
    this.tableEl = root.createDiv({ cls: "okvault-table-wrap" });

    this.registerEvent(this.app.metadataCache.on("resolved", () => this.rerender()));
    this.registerEvent(this.app.vault.on("delete", () => this.rerender()));
    this.registerEvent(this.app.vault.on("rename", () => this.rerender()));
    await this.reload();
  }

  /** Fetch review reasons from okvault (if reachable), then redraw. */
  async reload(): Promise<void> {
    this.reasons = new Map();
    this.reviewSource = "local";
    if (await this.plugin.ensureConnected()) {
      try {
        const r = await this.plugin.client.review(200);
        for (const it of r.items) this.reasons.set(it.path, it.reasons);
        this.reviewSource = "server";
      } catch (e) {
        this.plugin.markDisconnected((e as Error).message);
      }
    }
    this.render();
  }

  /** The filters, kept in the plugin's settings so they survive a restart. */
  private filter(): GraphFilter {
    return {
      hiddenTypes: new Set(this.plugin.settings.graphHiddenTypes),
      hiddenSections: new Set(this.plugin.settings.graphHiddenSections),
    };
  }

  private async setFilter(f: GraphFilter): Promise<void> {
    this.plugin.settings.graphHiddenTypes = [...f.hiddenTypes].sort();
    this.plugin.settings.graphHiddenSections = [...f.hiddenSections].sort();
    this.render();
    await this.plugin.saveSettings();
  }

  /** Filters that hide something present in the vault now. */
  private activeFilters(f: GraphFilter): number {
    return (
      this.catalog.types.filter((t) => f.hiddenTypes.has(t)).length +
      this.catalog.sections.filter((s) => f.hiddenSections.has(s.name)).length
    );
  }

  private render(): void {
    const all = this.plugin.localNotes();
    const cat = catalog(all);
    this.catalog = cat;
    const filter = this.filter();
    // Filtered notes are left out of the layout: no slot, no arc, no links, no row.
    const b = buildBundle(filterNotes(all, filter), this.reasons, RADIUS);
    this.bundle = b;
    // Colours come from the whole vault, so hiding a type never repaints the others.
    this.slot.clear();
    cat.types
      .filter((t) => t !== UNTYPED)
      .forEach((t, i) => this.slot.set(t, i < MAX_SLOTS ? `okvault-s${i + 1}` : "okvault-sother"));
    this.slot.set(UNTYPED, "okvault-sother");
    if (this.focusSection && !b.sections.some((s) => s.name === this.focusSection)) this.focusSection = null;

    const needs = [...b.nodes.values()].filter((n) => n.reasons.size).length;
    this.infoEl.empty();
    const filtered = b.nodes.size < cat.total;
    this.infoEl.createSpan({
      text:
        `${filtered ? `${b.nodes.size} of ${cat.total}` : b.nodes.size} notes · ` +
        `${b.sections.length} sections · ${b.edges.length} links · ${needs} need review`,
    });
    this.infoEl.createSpan({
      cls: "okvault-muted",
      text:
        this.reviewSource === "server"
          ? " (review status from okvault)"
          : " (okvault offline: drafts and broken links only)",
    });

    this.renderLegend(b);
    this.renderChart(b);
    this.renderTable(b);
  }

  private renderLegend(_b: Bundle): void {
    const el = this.legendEl;
    el.empty();
    const cat = this.catalog;
    const f = this.filter();
    const counts = cat.typeCount;
    const folded = cat.types.filter((t) => this.slot.get(t) === "okvault-sother" && t !== UNTYPED);
    const entries = cat.types.filter((t) => !folded.includes(t) && t !== UNTYPED);

    const row = (label: string) => {
      const r = el.createDiv({ cls: "okvault-filter-row" });
      r.createSpan({ cls: "okvault-muted okvault-filter-label", text: label });
      return r;
    };
    const chip = (parent: HTMLElement, label: string, count: number, off: boolean, swatch: string | null, toggle: () => void) => {
      const c = parent.createEl("button", {
        cls: `okvault-chip ${off ? "is-off" : ""}`,
        attr: { "aria-pressed": String(!off), title: off ? "Show in the map" : "Leave out of the map" },
      });
      if (swatch) c.createSpan({ cls: `okvault-swatch ${swatch}` });
      c.createSpan({ text: `${label} ${count}` });
      c.addEventListener("click", toggle);
    };

    const types = row("Types");
    const typeChip = (label: string, cls: string, ts: string[]) => {
      const off = ts.every((t) => f.hiddenTypes.has(t));
      chip(types, label, ts.reduce((s, t) => s + (counts.get(t) ?? 0), 0), off, cls, () => {
        for (const t of ts) off ? f.hiddenTypes.delete(t) : f.hiddenTypes.add(t);
        void this.setFilter(f);
      });
    };
    for (const t of entries) typeChip(t, this.slot.get(t)!, [t]);
    const other = [...folded, ...(counts.has(UNTYPED) ? [UNTYPED] : [])];
    if (other.length) {
      const label = folded.length ? (counts.has(UNTYPED) ? "Other & untyped" : "Other") : UNTYPED;
      typeChip(label, "okvault-sother", other);
    }

    if (cat.sections.length > 1) {
      const secs = row("Sections");
      for (const s of cat.sections) {
        const off = f.hiddenSections.has(s.name);
        chip(secs, s.name === ROOT ? "(root)" : `${s.name}/`, s.count, off, null, () => {
          off ? f.hiddenSections.delete(s.name) : f.hiddenSections.add(s.name);
          if (!off && this.focusSection === s.name) this.focusSection = null;
          void this.setFilter(f);
        });
      }
    }

    const active = this.activeFilters(f);
    if (active) {
      const reset = el.createEl("button", { cls: "okvault-filter-reset", text: `Show all (${active} hidden)` });
      reset.addEventListener("click", () => void this.setFilter({ hiddenTypes: new Set(), hiddenSections: new Set() }));
    }

    const key = el.createDiv({ cls: "okvault-legend-review" });
    key.createSpan({ cls: "okvault-muted", text: "Ring = needs review:" });
    for (const r of REASONS.slice(0, 3)) {
      const k = key.createSpan({ cls: "okvault-review-key" });
      k.createSpan({ cls: `okvault-ring-swatch okvault-r-${r.cls}` });
      k.createSpan({ text: `${r.glyph} ${r.label}` });
    }
    const k = key.createSpan({ cls: "okvault-review-key" });
    k.createSpan({ cls: "okvault-ring-swatch okvault-r-muted" });
    k.createSpan({ text: "✎ Draft / 👤 no owner" });
  }

  private renderChart(b: Bundle): void {
    const svg = this.svg;
    while (svg.firstChild) svg.removeChild(svg.firstChild);
    if (!b.nodes.size) {
      const t = el("text", { x: 0, y: 0, "text-anchor": "middle", class: "okvault-empty-text" });
      t.textContent = this.catalog.total
        ? "Every note is filtered out. Turn a type or section back on above."
        : "No OKF notes found. Check the okvault root setting and .vaultignore.";
      svg.appendChild(t);
      return;
    }
    const gEdges = el("g", { class: "okvault-edges" });
    const gArcs = el("g", { class: "okvault-arcs" });
    const gNodes = el("g", { class: "okvault-nodes" }) as SVGGElement;
    const gLabels = el("g", { class: "okvault-labels" });
    svg.append(gEdges, gArcs, gNodes, gLabels);

    const edgeEls: { from: string; to: string; el: SVGPathElement }[] = [];
    for (const e of b.edges) {
      const p = el("path", { d: e.d, class: `okvault-edge ${e.intra ? "is-intra" : ""}` }) as SVGPathElement;
      gEdges.appendChild(p);
      edgeEls.push({ from: e.from, to: e.to, el: p });
    }

    for (const s of b.sections) {
      const arc = el("path", {
        d: arcPath(s.start, s.end, RADIUS + 16, RADIUS + 26),
        class: `okvault-arc ${this.focusSection === s.name ? "is-focus" : ""}`,
      });
      arc.addEventListener("click", () => this.setFocus(this.focusSection === s.name ? null : s.name));
      arc.addEventListener("mouseenter", (ev) => {
        this.highlight(edgeEls, gNodes, (n) => n.section === s.name, (x, y) => b.nodes.get(x)!.section === s.name || b.nodes.get(y)!.section === s.name);
        this.showTip(ev as MouseEvent, sectionTip(s.name, s.count, s.internal, s.external, s.review));
      });
      arc.addEventListener("mousemove", (ev) => this.moveTip(ev as MouseEvent));
      arc.addEventListener("mouseleave", () => this.clearHighlight(edgeEls, gNodes));
      gArcs.appendChild(arc);
      // Many notes: no titles outside, so the section names go there.
      if (b.nodes.size > LABEL_LIMIT) {
        const mid = (s.start + s.end) / 2;
        const lp = polar(mid, RADIUS + 40);
        const lbl = el("text", {
          x: lp.x.toFixed(1),
          y: lp.y.toFixed(1),
          class: "okvault-arc-label",
          "text-anchor": anchorFor(mid),
          "dominant-baseline": "middle",
        });
        lbl.textContent = `${s.name} (${s.count})`;
        gLabels.appendChild(lbl);
      }
    }

    const showTitles = b.nodes.size <= LABEL_LIMIT;
    for (const n of b.nodes.values()) {
      const g = el("g", { class: "okvault-node", "data-path": n.path, tabindex: "0" });
      const reason = REASONS.find((r) => n.reasons.has(r.key));
      if (reason) {
        g.appendChild(el("circle", { cx: n.x.toFixed(1), cy: n.y.toFixed(1), r: 8.5, class: `okvault-ring okvault-r-${reason.cls}` }));
      }
      const r = 4.5 + Math.min(3, Math.sqrt(n.degree));
      g.appendChild(el("circle", { cx: n.x.toFixed(1), cy: n.y.toFixed(1), r: r.toFixed(1), class: `okvault-dot ${this.slot.get(n.type)}` }));
      // Hit target bigger than the mark.
      g.appendChild(el("circle", { cx: n.x.toFixed(1), cy: n.y.toFixed(1), r: 12, class: "okvault-hit-target" }));
      const enter = (ev: Event) => {
        const linked = new Set<string>([n.path]);
        for (const e of edgeEls) {
          if (e.from === n.path) linked.add(e.to);
          if (e.to === n.path) linked.add(e.from);
        }
        this.highlight(edgeEls, gNodes, (m) => linked.has(m.path), (x, y) => x === n.path || y === n.path);
        if (ev instanceof MouseEvent) this.showTip(ev, this.nodeTip(n, linked.size - 1));
      };
      g.addEventListener("mouseenter", enter);
      g.addEventListener("focus", enter);
      g.addEventListener("mousemove", (ev) => this.moveTip(ev as MouseEvent));
      g.addEventListener("mouseleave", () => this.clearHighlight(edgeEls, gNodes));
      g.addEventListener("blur", () => this.clearHighlight(edgeEls, gNodes));
      g.addEventListener("click", (ev) => this.plugin.openNote(n.path, (ev as MouseEvent).ctrlKey || (ev as MouseEvent).metaKey));
      g.addEventListener("keydown", (ev) => {
        if ((ev as KeyboardEvent).key === "Enter") this.plugin.openNote(n.path, false);
      });
      gNodes.appendChild(g);

      if (showTitles) {
        const lp = polar(n.angle, RADIUS + 34);
        const t = el("text", {
          x: lp.x.toFixed(1),
          y: lp.y.toFixed(1),
          class: "okvault-label",
          "text-anchor": anchorFor(n.angle),
          "dominant-baseline": "middle",
          "data-path": n.path,
        });
        t.textContent = truncate(n.title, 26);
        gLabels.appendChild(t);
      }
    }

    // Section names in the middle ring when titles take the outside.
    if (showTitles) {
      for (const s of b.sections) {
        const mid = (s.start + s.end) / 2;
        const p = polar(mid, RADIUS - 24);
        const t = el("text", {
          x: p.x.toFixed(1),
          y: p.y.toFixed(1),
          class: "okvault-section-label",
          "text-anchor": "middle",
          "dominant-baseline": "middle",
        });
        t.textContent = s.name;
        gLabels.appendChild(t);
      }
    }
    if (this.focusSection) this.applyFocus(edgeEls, gNodes);
  }

  private setFocus(name: string | null): void {
    this.focusSection = name;
    this.render();
  }

  private applyFocus(edges: { from: string; to: string; el: SVGPathElement }[], gNodes: SVGGElement): void {
    const b = this.bundle!;
    const s = this.focusSection!;
    this.highlight(edges, gNodes, (n) => n.section === s, (x, y) => b.nodes.get(x)!.section === s || b.nodes.get(y)!.section === s, true);
  }

  private highlight(
    edges: { from: string; to: string; el: SVGPathElement }[],
    gNodes: SVGGElement,
    nodeOn: (n: Node) => boolean,
    edgeOn: (from: string, to: string) => boolean,
    sticky = false,
  ): void {
    const b = this.bundle!;
    this.svg.classList.add("is-highlighting");
    for (const e of edges) e.el.classList.toggle("is-on", edgeOn(e.from, e.to));
    for (const g of Array.from(gNodes.children)) {
      const n = b.nodes.get((g as SVGGElement).dataset.path!);
      g.classList.toggle("is-on", !!n && nodeOn(n));
    }
    this.svg.querySelectorAll<SVGTextElement>(".okvault-label").forEach((t) => {
      const n = b.nodes.get(t.dataset.path!);
      t.classList.toggle("is-on", !!n && nodeOn(n));
    });
    if (!sticky) this.svg.classList.add("is-hover");
  }

  private clearHighlight(edges: { from: string; to: string; el: SVGPathElement }[], gNodes: SVGGElement): void {
    this.tip.hide();
    this.svg.classList.remove("is-hover");
    if (this.focusSection) {
      this.applyFocus(edges, gNodes);
      return;
    }
    this.svg.classList.remove("is-highlighting");
    for (const e of edges) e.el.classList.remove("is-on");
    for (const g of Array.from(gNodes.children)) g.classList.remove("is-on");
    this.svg.querySelectorAll(".okvault-label").forEach((t) => t.classList.remove("is-on"));
  }

  private nodeTip(n: Node, links: number): HTMLElement {
    const d = createDiv();
    d.createDiv({ cls: "okvault-tip-title", text: n.title });
    const meta = d.createDiv({ cls: "okvault-tip-meta" });
    meta.createSpan({ cls: `okvault-swatch ${this.slot.get(n.type)}` });
    meta.createSpan({ text: `${n.type} · ${n.section === ROOT ? "root" : n.section}/` });
    d.createDiv({ cls: "okvault-tip-row", text: `${links} linked note${links === 1 ? "" : "s"}` });
    for (const r of REASONS) {
      if (n.reasons.has(r.key)) d.createDiv({ cls: `okvault-tip-row okvault-t-${r.cls}`, text: `${r.glyph} ${r.label}` });
    }
    d.createDiv({ cls: "okvault-tip-path", text: n.path });
    return d;
  }

  private showTip(ev: MouseEvent, content: HTMLElement): void {
    this.tip.empty();
    this.tip.appendChild(content);
    this.tip.show();
    this.moveTip(ev);
  }

  private moveTip(ev: MouseEvent): void {
    const box = (this.tip.parentElement as HTMLElement).getBoundingClientRect();
    let x = ev.clientX - box.left + 14;
    const y = ev.clientY - box.top + 14;
    if (x + this.tip.offsetWidth > box.width) x = ev.clientX - box.left - this.tip.offsetWidth - 14;
    this.tip.style.left = `${Math.max(0, x)}px`;
    this.tip.style.top = `${y}px`;
  }

  /** The table view: the same data, readable without colour or hover. */
  private renderTable(b: Bundle): void {
    const wrap = this.tableEl;
    wrap.empty();
    const table = wrap.createEl("table", { cls: "okvault-table" });
    const head = table.createEl("thead").createEl("tr");
    for (const h of ["Section", "Notes", "Types", "Links inside", "Links out", "Need review"]) head.createEl("th", { text: h });
    const body = table.createEl("tbody");
    for (const s of b.sections) {
      const tr = body.createEl("tr", { cls: this.focusSection === s.name ? "is-focus" : "" });
      tr.addEventListener("click", () => this.setFocus(this.focusSection === s.name ? null : s.name));
      const name = tr.createEl("td");
      const link = name.createEl("a", { text: s.name === ROOT ? "(root)" : `${s.name}/` });
      link.addEventListener("click", (e) => {
        e.stopPropagation();
        this.plugin.openNote(s.name === ROOT ? "index.md" : `${s.name}/index.md`, e.ctrlKey || e.metaKey);
      });
      tr.createEl("td", { text: String(s.count), cls: "is-num" });
      const types = tr.createEl("td");
      for (const [t, c] of [...s.types.entries()].sort((x, y) => y[1] - x[1])) {
        const tag = types.createSpan({ cls: "okvault-type-count" });
        tag.createSpan({ cls: `okvault-swatch ${this.slot.get(t)}` });
        tag.createSpan({ text: `${t} ${c}` });
      }
      tr.createEl("td", { text: String(s.internal), cls: "is-num" });
      tr.createEl("td", { text: String(s.external), cls: "is-num" });
      tr.createEl("td", { text: String(s.review), cls: `is-num ${s.review ? "okvault-t-warning" : ""}` });
    }
  }
}

function sectionTip(name: string, count: number, internal: number, external: number, review: number): HTMLElement {
  const d = createDiv();
  d.createDiv({ cls: "okvault-tip-title", text: name === ROOT ? "(root)" : `${name}/` });
  d.createDiv({ cls: "okvault-tip-row", text: `${count} note${count === 1 ? "" : "s"}` });
  d.createDiv({ cls: "okvault-tip-row", text: `${internal} links inside · ${external} to other sections` });
  if (review) d.createDiv({ cls: "okvault-tip-row okvault-t-warning", text: `⚠ ${review} need review` });
  d.createDiv({ cls: "okvault-tip-path", text: "Click to focus" });
  return d;
}

function el(tag: keyof SVGElementTagNameMap, attrs: Record<string, string | number>): SVGElement {
  return createSvg(tag, { attr: attrs });
}

/** Text on the right half reads outward from the start, on the left half it ends at the point. */
function anchorFor(angle: number): string {
  const s = Math.sin(angle);
  if (Math.abs(s) < 0.15) return "middle";
  return s > 0 ? "start" : "end";
}

function truncate(s: string, n: number): string {
  return s.length > n ? s.slice(0, n - 1) + "…" : s;
}
