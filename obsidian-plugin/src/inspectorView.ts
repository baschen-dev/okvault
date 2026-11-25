import { ItemView, TFile, WorkspaceLeaf, debounce, setIcon } from "obsidian";
import type { Hit, NoteDetail } from "./client";
import type OkvaultPlugin from "./main";
import { isConceptPath } from "./bundle";
import { kebab, shortDate, ticketsOf, trustOf } from "./noteFacts";
import type { Finding } from "./server";

export const INSPECTOR_VIEW = "okvault-inspector";

/** Sidebar that follows the open note: can I trust it, who owns it, is it well formed, is it a duplicate? */
export class InspectorView extends ItemView {
  private file: TFile | null = null;
  private detail: NoteDetail | null = null;
  private similar: Hit[] = [];
  private offline = "";
  private seq = 0;
  private busy = false;

  private reload = debounce(() => void this.loadNote(), 600, true);

  constructor(
    leaf: WorkspaceLeaf,
    private plugin: OkvaultPlugin,
  ) {
    super(leaf);
  }

  getViewType(): string {
    return INSPECTOR_VIEW;
  }
  getDisplayText(): string {
    return "okvault note";
  }
  override getIcon(): string {
    return "shield-check";
  }

  override async onOpen(): Promise<void> {
    this.contentEl.addClass("okvault-inspector");
    this.registerEvent(this.app.workspace.on("file-open", () => this.follow()));
    this.registerEvent(this.app.workspace.on("active-leaf-change", () => this.follow()));
    this.registerEvent(
      this.app.metadataCache.on("changed", (f) => {
        if (f.path === this.file?.path) this.reload();
      }),
    );
    this.registerEvent(
      this.app.vault.on("rename", (f, old) => {
        if (old === this.file?.path && f instanceof TFile) {
          this.file = f;
          this.reload();
        }
      }),
    );
    this.plugin.requestLint();
    this.follow();
  }

  /** Called by the plugin when lint results or the connection change. */
  refresh(): void {
    this.render();
  }

  private follow(): void {
    const f = this.app.workspace.getActiveFile();
    if (!f || f.extension !== "md") return; // keep showing the last note while a sidebar has focus
    if (f.path === this.file?.path && this.detail) return;
    this.file = f;
    this.detail = null;
    this.similar = [];
    this.render();
    void this.loadNote();
  }

  private okPath(): string | null {
    return this.file ? this.plugin.fromObsidian(this.file.path) : null;
  }

  private async loadNote(): Promise<void> {
    const p = this.okPath();
    const seq = ++this.seq;
    this.offline = "";
    if (!p || !isConceptPath(p) || this.plugin.ignore.match(p)) return this.render();
    if (!(await this.plugin.ensureConnected())) {
      this.offline = this.plugin.lastError;
      return this.render();
    }
    try {
      const d = await this.plugin.client.get(p);
      const fm = this.fm();
      const aliases = Array.isArray(fm.aliases) ? fm.aliases.join(" ") : typeof fm.aliases === "string" ? fm.aliases : "";
      const hits = await this.plugin.client.search({ query: `${d.title} ${aliases}`.trim(), limit: 6 });
      if (seq !== this.seq) return;
      this.detail = d;
      this.similar = hits.filter((h) => h.path !== p).slice(0, 4);
    } catch (e) {
      if (seq !== this.seq) return;
      this.offline = (e as Error).message;
    }
    this.render();
  }

  private fm(): Record<string, unknown> {
    return (this.file && this.app.metadataCache.getFileCache(this.file)?.frontmatter) ?? {};
  }

  private render(): void {
    const el = this.contentEl;
    el.empty();
    const f = this.file;
    const p = this.okPath();
    if (!f) {
      el.createDiv({ cls: "okvault-empty", text: "Open a note to see what okvault knows about it." });
      return;
    }
    if (!p) {
      el.createDiv({ cls: "okvault-empty", text: `${f.path} is outside the okvault root (see the plugin's settings).` });
      return;
    }
    if (this.plugin.ignore.match(p)) {
      el.createDiv({ cls: "okvault-empty", text: `${p} is excluded by .vaultignore: okvault does not index it.` });
      return;
    }
    if (!isConceptPath(p)) {
      el.createDiv({ cls: "okvault-empty", text: `${p} is not a note okvault indexes (index.md, log.md and _folders are maintained or skipped).` });
      return;
    }
    const fm = this.fm();
    const head = el.createDiv({ cls: "okvault-insp-head" });
    head.createDiv({ cls: "okvault-insp-title", text: (fm.title as string) || this.detail?.title || f.basename });
    const meta = head.createDiv({ cls: "okvault-insp-meta" });
    meta.createSpan({ cls: "okvault-type", text: (fm.type as string) || "untyped" });
    // <s> strikes a deprecated note's status through by itself.
    if (fm.status) meta.createEl(fm.status === "deprecated" ? "s" : "span", { cls: `okvault-type okvault-status-chip-${fm.status}`, text: String(fm.status) });
    meta.createSpan({ cls: "okvault-path", text: p });
    if (this.offline) el.createDiv({ cls: "okvault-status okvault-status-err", text: `okvault not reachable (${this.offline}); showing what the frontmatter says.` });

    this.renderTrust(el, fm, p);
    this.renderOwnership(el, fm);
    this.renderFormat(el, fm, p);
    this.renderLinks(el);
    this.renderSimilar(el);
  }

  // ---- trust ----

  private renderTrust(el: HTMLElement, fm: Record<string, unknown>, p: string): void {
    const t = trustOf(fm);
    const card = this.card(el, "Trust");
    const row = (icon: string, text: string, cls = "") => {
      const r = card.createDiv({ cls: `okvault-insp-row ${cls}` });
      setIcon(r.createSpan({ cls: "okvault-insp-icon" }), icon);
      r.createSpan({ text });
    };
    if (t.generated) row("bot", `Written by ${t.generated.by} (agent) on ${shortDate(t.generated.at)}`);
    else row("user", "Written by a person (no agent stamp)");
    if (t.lastVerified) {
      const others = t.verified.length > 1 ? ` · ${t.verified.length} checks in total` : "";
      row("badge-check", `Checked by ${t.lastVerified.by} on ${shortDate(t.lastVerified.at)}${others}`);
    } else row("circle-dashed", "Never checked by a person", t.generated ? "okvault-t-warning" : "");
    if (t.unverified && t.lastVerified) row("alert-triangle", "Changed by an agent since the last check", "okvault-t-warning");
    if (t.staleAfterInvalid) row("alert-triangle", "stale_after is not a date (okvault lint warns)", "okvault-t-warning");
    else if (t.staleAfter && t.daysLeft !== undefined) {
      if (t.stale) row("clock-alert", `Stale since ${shortDate(t.staleAfter)} (${-t.daysLeft} days)`, "okvault-t-serious");
      else row("clock", `Fresh until ${shortDate(t.staleAfter)} (${t.daysLeft} days)`, t.daysLeft < 14 ? "okvault-t-warning" : "");
    } else row("clock", "No review date (stale_after); checking it sets one");
    const b = card.createEl("button", { text: this.busy ? "Recording…" : "Mark as checked", cls: t.unverified || t.stale ? "mod-cta" : "" });
    b.disabled = this.busy;
    b.setAttr("title", "okvault verify: records that you read this note and it is correct, and moves its review date ahead");
    b.addEventListener("click", async () => {
      this.busy = true;
      this.render();
      await this.plugin.verify(p);
      this.busy = false;
      this.detail = null;
      void this.loadNote();
    });
  }

  // ---- owners and tickets ----

  private renderOwnership(el: HTMLElement, fm: Record<string, unknown>): void {
    const card = this.card(el, "Owners and tickets");
    const own = Array.isArray(fm.owners) ? (fm.owners as string[]) : typeof fm.owners === "string" ? [fm.owners] : [];
    const owners = this.detail?.owners ?? own;
    const o = card.createDiv({ cls: "okvault-insp-row" });
    setIcon(o.createSpan({ cls: "okvault-insp-icon" }), "users");
    if (owners.length) {
      o.createSpan({ text: owners.join(", ") });
      if (!own.length && this.detail?.owners?.length) o.createSpan({ cls: "okvault-muted", text: " (folder default)" });
    } else o.createSpan({ cls: "okvault-t-warning", text: "No owner" });

    const links = this.detail?.tickets ?? ticketsOf(fm).map((key) => ({ key, url: undefined as string | undefined }));
    const t = card.createDiv({ cls: "okvault-insp-row okvault-chips" });
    setIcon(t.createSpan({ cls: "okvault-insp-icon" }), "ticket");
    if (!links.length) t.createSpan({ cls: "okvault-muted", text: "No tickets" });
    for (const tk of links) {
      const chip = t.createSpan({ cls: "okvault-chip-link" });
      const a = chip.createEl("a", { text: tk.key, attr: { title: "Open in the ticket workspace" } });
      a.addEventListener("click", () => this.plugin.openTicket(tk.key));
      if (tk.url) {
        const ext = chip.createEl("a", { cls: "okvault-ext", href: tk.url, attr: { title: tk.url, "aria-label": "Open in the tracker" } });
        setIcon(ext, "external-link");
      }
    }
  }

  // ---- format (okvault lint) ----

  private renderFormat(el: HTMLElement, fm: Record<string, unknown>, p: string): void {
    const card = this.card(el, "Format");
    const lint = this.plugin.lint;
    const findings: Finding[] = lint.findings.get(p) ?? [];
    if (lint.running && !lint.at) {
      card.createDiv({ cls: "okvault-muted", text: "Checking with okvault lint…" });
    } else if (lint.error) {
      card.createDiv({ cls: "okvault-muted", text: `okvault lint unavailable: ${lint.error}` });
    } else if (!findings.length) {
      const r = card.createDiv({ cls: "okvault-insp-row" });
      setIcon(r.createSpan({ cls: "okvault-insp-icon" }), "check");
      r.createSpan({ text: "Conforms to OKF; no warnings" });
    }
    for (const f of findings) {
      const r = card.createDiv({ cls: `okvault-finding okvault-finding-${f.severity}` });
      r.createDiv({ text: f.message });
      this.fixFor(r, f, fm, p);
    }
    // Recommended keys lint mentions only once, offered even without a finding.
    if (!fm.title && !findings.some((f) => f.message.includes("title"))) {
      const r = card.createDiv({ cls: "okvault-finding okvault-finding-hint" });
      r.createDiv({ text: "No title: okvault derives one from the file name." });
      this.button(r, "Set title", () => this.setFrontmatter("title", humanize(this.file!.basename)));
    }
  }

  /** Quick fixes for the lint messages that have an obvious one. */
  private fixFor(r: HTMLElement, f: Finding, fm: Record<string, unknown>, _p: string): void {
    const m = f.message;
    if (m.includes("no `type`")) {
      const types = [...new Set(this.plugin.localNotes().map((n) => n.type).filter(Boolean))].sort();
      const sel = r.createEl("select", { cls: "dropdown" });
      for (const t of types) sel.createEl("option", { text: t, attr: { value: t } });
      const guess = types.find((t) => t.toLowerCase() === "faq" && /\?\s*$/.test(String(fm.title ?? this.file?.basename))) ?? types[0];
      if (guess) sel.value = guess;
      this.button(r, "Set type", () => this.setFrontmatter("type", sel.value));
    } else if (m.includes("no `description`")) {
      const input = r.createEl("input", { type: "text", attr: { placeholder: "One sentence: what this note answers" } });
      this.button(r, "Set description", () => input.value.trim() && this.setFrontmatter("description", input.value.trim()));
    } else if (m.startsWith("file name")) {
      const name = kebab(String(fm.title ?? this.file!.basename)) || kebab(this.file!.basename);
      this.button(r, `Rename to ${name}.md`, () => this.rename(name));
    } else if (m.startsWith("stale")) {
      r.createDiv({ cls: "okvault-muted", text: "Read it, fix what changed, then Mark as checked above." });
    } else {
      const other = /also answered by (\S+)/.exec(m)?.[1];
      if (other) this.button(r, "Open the other FAQ", () => this.plugin.openNote(other, false));
    }
  }

  private button(r: HTMLElement, text: string, fn: () => unknown): void {
    const b = r.createEl("button", { text, cls: "okvault-fix" });
    b.addEventListener("click", async () => {
      b.disabled = true;
      await fn();
    });
  }

  private async setFrontmatter(key: string, value: string): Promise<void> {
    if (!this.file) return;
    await this.app.fileManager.processFrontMatter(this.file, (fm) => {
      fm[key] = value;
    });
    this.plugin.requestLint();
  }

  private async rename(name: string): Promise<void> {
    const f = this.file;
    if (!f) return;
    const dir = f.parent && !f.parent.isRoot() ? `${f.parent.path}/` : "";
    // Obsidian updates links to the note (the vault sets alwaysUpdateLinks).
    await this.app.fileManager.renameFile(f, `${dir}${name}.md`);
    this.plugin.requestLint();
  }

  // ---- links ----

  private renderLinks(el: HTMLElement): void {
    const d = this.detail;
    if (!d) return;
    const card = this.card(el, "Links");
    const list = (label: string, paths: string[] | undefined, cls = "") => {
      const n = paths?.length ?? 0;
      const det = card.createEl("details", { cls: `okvault-insp-links ${cls}` });
      det.open = cls === "is-broken" || (label === "Linked from" && n > 0 && n <= 5);
      det.createEl("summary", { text: `${label} (${n})` });
      for (const p of paths ?? []) {
        const a = det.createDiv({ cls: "okvault-insp-link" }).createEl("a", { text: p });
        if (cls !== "is-broken") a.addEventListener("click", (e) => this.plugin.openNote(p, e.ctrlKey || e.metaKey));
      }
    };
    list("Linked from", d.backlinks);
    list("Links to", d.links);
    if (d.broken_links?.length) list("Broken", d.broken_links, "is-broken");
    if (!d.backlinks?.length) card.createDiv({ cls: "okvault-hint", text: "Nothing links here: agents still find it by search, people browsing will not." });
  }

  // ---- similar notes ----

  private renderSimilar(el: HTMLElement): void {
    if (!this.detail) return;
    const card = this.card(el, "Similar notes");
    if (!this.similar.length) {
      card.createDiv({ cls: "okvault-muted", text: "No similar notes: this looks like the only note on its topic." });
      return;
    }
    card.createDiv({ cls: "okvault-hint", text: "Ranked by the same search agents use. Merge near-duplicates rather than keeping two answers." });
    for (const h of this.similar) {
      const row = card.createDiv({ cls: "okvault-hit" });
      row.addEventListener("click", (e) => this.plugin.openNote(h.path, e.ctrlKey || e.metaKey));
      const top = row.createDiv({ cls: "okvault-hit-top" });
      top.createSpan({ cls: "okvault-hit-title", text: h.title || h.path });
      top.createSpan({ cls: "okvault-type", text: h.type || "untyped" });
      row.createDiv({ cls: "okvault-path", text: h.path });
    }
  }

  private card(el: HTMLElement, title: string): HTMLElement {
    const c = el.createDiv({ cls: "okvault-card" });
    c.createDiv({ cls: "okvault-card-title", text: title });
    return c;
  }
}

function humanize(base: string): string {
  const s = base.replace(/[-_]+/g, " ").trim();
  return s.charAt(0).toUpperCase() + s.slice(1);
}
