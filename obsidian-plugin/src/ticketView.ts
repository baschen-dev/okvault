import { ItemView, Notice, TFile, WorkspaceLeaf, setIcon } from "obsidian";
import type { Entry, Hit, TicketContext } from "./client";
import type OkvaultPlugin from "./main";
import { folderFor, kebab, normalizeKey, notePath, ticketsOf } from "./noteFacts";
import { Template, createNote, templates } from "./notes";

export const TICKET_VIEW = "okvault-ticket";

/**
 * The ticket loop from okvault's team workflow: starting PROJ-123, read what
 * the team knows (kb_ticket); finishing it, record what you learned in a note
 * that lists the ticket.
 */
export class TicketView extends ItemView {
  private key = "";
  private ctx: TicketContext | null = null;
  private error = "";
  private loading = false;
  private recent: string[] = [];
  private input!: HTMLInputElement;
  private body!: HTMLElement;
  private seq = 0;

  constructor(
    leaf: WorkspaceLeaf,
    private plugin: OkvaultPlugin,
  ) {
    super(leaf);
  }

  getViewType(): string {
    return TICKET_VIEW;
  }
  getDisplayText(): string {
    return this.key ? `okvault ${this.key}` : "okvault tickets";
  }
  override getIcon(): string {
    return "ticket";
  }

  override async onOpen(): Promise<void> {
    const root = this.contentEl;
    root.empty();
    root.addClass("okvault-tickets");
    const bar = root.createDiv({ cls: "okvault-searchbar" });
    this.input = bar.createEl("input", { type: "search", cls: "okvault-input", attr: { placeholder: "Ticket key, e.g. PROJ-123" } });
    this.input.addEventListener("keydown", (e) => {
      if (e.key === "Enter") void this.show(this.input.value);
    });
    this.body = root.createDiv();
    this.registerEvent(this.app.metadataCache.on("resolved", () => !this.key && this.render()));
    this.render();
  }

  focus(): void {
    this.input?.focus();
  }

  /** Load a ticket's context and show it. */
  async show(raw: string): Promise<void> {
    const k = normalizeKey(raw);
    if (!k) {
      this.error = `"${raw.trim()}" is not a ticket key (e.g. PROJ-123)`;
      this.ctx = null;
      return this.render();
    }
    this.key = k;
    this.input.value = k;
    this.recent = [k, ...this.recent.filter((x) => x !== k)].slice(0, 8);
    this.error = "";
    this.ctx = null;
    this.loading = true;
    (this.leaf as unknown as { updateHeader?: () => void } | undefined)?.updateHeader?.();
    this.render();
    const seq = ++this.seq;
    if (!(await this.plugin.ensureConnected())) {
      this.loading = false;
      this.error = `okvault is not reachable (${this.plugin.lastError}). Notes listing ${k} are shown from the vault.`;
      return this.render();
    }
    try {
      const ctx = await this.plugin.client.ticket(k);
      if (seq !== this.seq) return;
      this.ctx = ctx;
    } catch (e) {
      if (seq !== this.seq) return;
      this.error = (e as Error).message;
    }
    this.loading = false;
    this.render();
  }

  private render(): void {
    const el = this.body;
    if (!el) return;
    el.empty();
    if (this.error) el.createDiv({ cls: "okvault-status okvault-status-err", text: this.error });
    if (!this.key) return this.renderIndex(el);

    const back = el.createEl("a", { cls: "okvault-back", text: "← All documented tickets" });
    back.addEventListener("click", () => {
      this.key = "";
      this.ctx = null;
      this.error = "";
      this.input.value = "";
      this.render();
    });
    if (this.loading) {
      el.createDiv({ cls: "okvault-muted", text: `Loading ${this.key}…` });
      return;
    }
    const c = this.ctx;
    this.renderTicket(el, c);
    if (c) {
      this.entries(el, "Linked notes", "Notes listing this ticket in their tickets", c.linked);
      this.entries(el, "Mentioned in", "Notes that mention the key in their text", c.mentioned);
      this.hits(el, "Probably relevant", "Notes matching the ticket's summary", c.suggested);
      if (c.hint) el.createDiv({ cls: "okvault-hint", text: c.hint });
    } else {
      // offline: the vault's own record
      const local = this.plugin.localNotes().filter((n) => ticketsOf(this.frontmatter(n.path)).includes(this.key));
      this.entries(el, "Linked notes", "Notes listing this ticket in their tickets", local.map((n) => ({ path: n.path, title: n.title, type: n.type })));
    }
    this.renderRecord(el);
  }

  private renderTicket(el: HTMLElement, c: TicketContext | null): void {
    const card = el.createDiv({ cls: "okvault-card okvault-ticket-card" });
    const head = card.createDiv({ cls: "okvault-card-head" });
    const url = c?.ticket?.url ?? c?.url;
    const title = head.createDiv({ cls: "okvault-card-title" });
    if (url) title.createEl("a", { text: this.key, href: url, attr: { title: url } });
    else title.setText(this.key);
    const t = c?.ticket;
    if (t?.status) head.createSpan({ cls: `okvault-ticket-status okvault-cat-${kebab(t.status_category ?? "")}`, text: t.status });
    if (!t) {
      card.createDiv({ cls: "okvault-hint", text: c?.tracker_note ? `Ticket details unavailable: ${c.tracker_note}` : "No tracker configured; showing what the vault knows." });
      return;
    }
    card.createDiv({ cls: "okvault-ticket-summary", text: t.summary });
    const facts = [t.type, t.priority && `priority ${t.priority}`, t.assignee ? `assigned to ${t.assignee}` : "unassigned", t.parent && `parent ${t.parent}`, t.updated && `updated ${t.updated.slice(0, 10)}`]
      .filter(Boolean)
      .join(" · ");
    card.createDiv({ cls: "okvault-hit-desc", text: facts });
    if (t.labels?.length) {
      const l = card.createDiv({ cls: "okvault-reasons" });
      for (const x of t.labels) l.createSpan({ cls: "okvault-reason", text: x });
    }
    if (t.description) {
      const d = card.createEl("details");
      d.createEl("summary", { text: "Description" });
      d.createDiv({ cls: "okvault-ticket-text", text: t.description });
    }
    if (t.recent_comments?.length) {
      const d = card.createEl("details");
      d.createEl("summary", { text: `Recent comments (${t.recent_comments.length})` });
      for (const cm of t.recent_comments) {
        const x = d.createDiv({ cls: "okvault-ticket-comment" });
        x.createDiv({ cls: "okvault-muted", text: `${cm.author} · ${cm.created.slice(0, 10)}` });
        x.createDiv({ cls: "okvault-ticket-text", text: cm.body });
      }
    }
  }

  private entries(el: HTMLElement, title: string, tip: string, list: Entry[] | undefined): void {
    const sec = el.createDiv({ cls: "okvault-section" });
    sec.createDiv({ cls: "okvault-section-title", text: `${title} (${list?.length ?? 0})`, attr: { title: tip } });
    if (!list?.length) {
      sec.createDiv({ cls: "okvault-muted", text: "None" });
      return;
    }
    for (const n of list) this.row(sec, n.path, n.title, n.type, n.description);
  }

  private hits(el: HTMLElement, title: string, tip: string, list: Hit[] | undefined): void {
    if (!list?.length) return;
    const sec = el.createDiv({ cls: "okvault-section" });
    sec.createDiv({ cls: "okvault-section-title", text: title, attr: { title: tip } });
    for (const h of list) this.row(sec, h.path, h.title, h.type, h.description);
  }

  private row(sec: HTMLElement, path: string, title: string, type: string, desc?: string): void {
    const row = sec.createDiv({ cls: "okvault-hit" });
    row.addEventListener("click", (e) => this.plugin.openNote(path, e.ctrlKey || e.metaKey));
    const top = row.createDiv({ cls: "okvault-hit-top" });
    top.createSpan({ cls: "okvault-hit-title", text: title || path });
    if (type) top.createSpan({ cls: "okvault-type", text: type });
    if (desc) row.createDiv({ cls: "okvault-hit-desc", text: desc });
    row.createDiv({ cls: "okvault-path", text: path });
  }

  // ---- record what we learned ----

  private renderRecord(el: HTMLElement): void {
    const card = el.createDiv({ cls: "okvault-card" });
    card.createDiv({ cls: "okvault-card-title", text: "Record what we learned" });
    card.createDiv({
      cls: "okvault-hint",
      text: `Finishing ${this.key}? Write down the decision, gotcha or answer for the team. Prefer updating a linked note above to adding a near-duplicate.`,
    });
    const templates = this.templates();
    const form = card.createDiv({ cls: "okvault-record" });
    const sel = form.createEl("select", { cls: "dropdown" });
    for (const t of templates) sel.createEl("option", { text: t.label, attr: { value: t.path } });
    const title = form.createEl("input", { type: "text", attr: { placeholder: "Title, e.g. Why we pinned the Jira API to v2" } });
    const folder = form.createEl("input", { type: "text", attr: { placeholder: "folder" } });
    const target = card.createDiv({ cls: "okvault-path" });
    const suggest = () => {
      const t = templates.find((x) => x.path === sel.value);
      if (t && !folder.dataset.touched) folder.value = folderFor(t.type, this.plugin.localNotes());
      target.setText(`→ ${notePath(folder.value, title.value) ?? "…"}`);
    };
    sel.addEventListener("change", suggest);
    title.addEventListener("input", suggest);
    folder.addEventListener("input", () => {
      folder.dataset.touched = "1";
      suggest();
    });
    suggest();
    const b = form.createEl("button", { text: "Create note", cls: "mod-cta" });
    b.addEventListener("click", async () => {
      const t = templates.find((x) => x.path === sel.value);
      if (t && title.value.trim()) await this.create(t, title.value.trim(), folder.value);
      else new Notice("okvault: give the note a title");
    });
    if (!templates.length) card.createDiv({ cls: "okvault-muted", text: "No templates in _templates/: the note starts from plain frontmatter." });
  }

  /** Templates, the usual outcomes of a ticket first. */
  private templates(): Template[] {
    const out = templates(this.plugin);
    if (!out.length) out.push({ path: "", label: "Note", type: "Reference" });
    const rank = (t: string) => {
      const i = ["Decision", "FAQ", "Playbook"].indexOf(t);
      return i < 0 ? 9 : i;
    };
    return out.sort((a, b) => rank(a.type) - rank(b.type) || a.label.localeCompare(b.label));
  }

  private async create(t: Template, title: string, folder: string): Promise<void> {
    const key = this.key;
    const r = await createNote(this.plugin, {
      template: t,
      title,
      folder,
      edit: (fm) => {
        const list = ticketsOf(fm);
        if (!list.includes(key)) list.push(key);
        fm.tickets = list;
      },
    });
    if (!r.file) {
      new Notice(`okvault: ${r.error}${r.error?.endsWith("exists") ? `; open it and add ${key} to its tickets instead` : ""}.`);
      return;
    }
    await this.app.workspace.getLeaf("tab").openFile(r.file);
    new Notice(`okvault: created ${r.rel}, linked to ${key}`);
  }

  // ---- the index: tickets the vault documents ----

  private renderIndex(el: HTMLElement): void {
    if (this.recent.length) {
      const r = el.createDiv({ cls: "okvault-section" });
      r.createDiv({ cls: "okvault-section-title", text: "Recent" });
      const chips = r.createDiv({ cls: "okvault-chips" });
      for (const k of this.recent) chips.createEl("a", { cls: "okvault-chip-link", text: k }).addEventListener("click", () => this.show(k));
    }
    const byKey = new Map<string, { path: string; title: string }[]>();
    for (const n of this.plugin.localNotes()) {
      for (const k of ticketsOf(this.frontmatter(n.path))) {
        if (!byKey.has(k)) byKey.set(k, []);
        byKey.get(k)!.push({ path: n.path, title: n.title });
      }
    }
    const sec = el.createDiv({ cls: "okvault-section" });
    sec.createDiv({ cls: "okvault-section-title", text: `Documented tickets (${byKey.size})` });
    if (!byKey.size) {
      sec.createDiv({
        cls: "okvault-muted",
        text: "No note lists a ticket yet. When you finish a ticket, open it here and record what you learned; the note gets tickets: [KEY].",
      });
      return;
    }
    const keys = [...byKey.keys()].sort((a, b) => byKey.get(b)!.length - byKey.get(a)!.length || a.localeCompare(b, undefined, { numeric: true }));
    for (const k of keys) {
      const notes = byKey.get(k)!;
      const row = sec.createDiv({ cls: "okvault-hit" });
      row.addEventListener("click", () => this.show(k));
      const top = row.createDiv({ cls: "okvault-hit-top" });
      const name = top.createSpan({ cls: "okvault-hit-title" });
      setIcon(name.createSpan({ cls: "okvault-insp-icon" }), "ticket");
      name.appendText(k);
      top.createSpan({ cls: "okvault-type", text: `${notes.length} note${notes.length > 1 ? "s" : ""}` });
      row.createDiv({ cls: "okvault-hit-desc", text: notes.map((n) => n.title).join(" · ") });
    }
  }

  private frontmatter(okPath: string): Record<string, unknown> | undefined {
    const f = this.app.vault.getAbstractFileByPath(this.plugin.toObsidian(okPath));
    return f instanceof TFile ? this.app.metadataCache.getFileCache(f)?.frontmatter : undefined;
  }
}
