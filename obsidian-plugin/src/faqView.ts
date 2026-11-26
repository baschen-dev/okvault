import { ItemView, MarkdownRenderer, WorkspaceLeaf, setIcon } from "obsidian";
import type { Answer, Hit } from "./client";
import type OkvaultPlugin from "./main";
import { act, background } from "./act";

export const FAQ_VIEW = "okvault-faq";

/** Sidebar: ask the vault a question the way an agent does (kb_ask), then search. */
export class FaqView extends ItemView {
  private input!: HTMLInputElement;
  private statusEl!: HTMLElement;
  private resultsEl!: HTMLElement;
  private timer: number | null = null;
  private seq = 0;

  constructor(
    leaf: WorkspaceLeaf,
    private plugin: OkvaultPlugin,
  ) {
    super(leaf);
  }

  getViewType(): string {
    return FAQ_VIEW;
  }
  getDisplayText(): string {
    return "okvault FAQ";
  }
  override getIcon(): string {
    return "message-circle-question";
  }

  override async onOpen(): Promise<void> {
    const root = this.contentEl;
    root.empty();
    root.addClass("okvault-faq");

    const bar = root.createDiv({ cls: "okvault-searchbar" });
    this.input = bar.createEl("input", {
      type: "search",
      placeholder: "Ask a question, e.g. how do I add an FAQ?",
      cls: "okvault-input",
    });
    this.input.addEventListener("input", () => this.schedule());
    this.input.addEventListener("keydown", (e) => {
      if (e.key === "Enter") background(this.run());
    });
    this.statusEl = root.createDiv({ cls: "okvault-status" });
    this.resultsEl = root.createDiv({ cls: "okvault-results" });

    // Links inside rendered answers open the note they point to.
    this.registerDomEvent(this.resultsEl, "click", (e) => {
      const a = (e.target as HTMLElement).closest<HTMLAnchorElement>("a.internal-link");
      if (!a) return;
      e.preventDefault();
      const href = a.dataset.href ?? a.getAttribute("href") ?? "";
      const src = a.closest("[data-source]")?.getAttribute("data-source") ?? "";
      background(this.app.workspace.openLinkText(href, src, e.ctrlKey || e.metaKey));
    });

    await this.run();
  }

  override async onClose(): Promise<void> {
    if (this.timer !== null) window.clearTimeout(this.timer);
  }

  /** Prefill the box (from the command palette or the selection) and ask. */
  setQuery(q: string): void {
    this.input.value = q;
    background(this.run());
    this.input.focus();
  }

  focus(): void {
    this.input?.focus();
  }

  /** Re-run the current query, e.g. after reconnecting. */
  refresh(): void {
    background(this.run());
  }

  private schedule(): void {
    if (this.timer !== null) window.clearTimeout(this.timer);
    this.timer = window.setTimeout(act(() => this.run()), 250);
  }

  private async run(): Promise<void> {
    if (this.timer !== null) window.clearTimeout(this.timer);
    this.timer = null;
    const q = this.input.value.trim();
    const seq = ++this.seq;
    const p = this.plugin;

    if (!(await p.ensureConnected())) {
      if (seq !== this.seq) return;
      this.renderOffline(q);
      return;
    }
    try {
      if (!q) {
        const faqs = await p.client.search({ query: "", type: "FAQ", limit: 50 });
        if (seq !== this.seq) return;
        this.setStatus(`${faqs.length} FAQ${faqs.length === 1 ? "" : "s"} in the vault`, "ok");
        this.resultsEl.empty();
        this.renderHits(this.resultsEl, "All FAQs", faqs, false);
        return;
      }
      const [answer, hits] = await Promise.all([
        p.client.ask(q),
        p.client.search({ query: q, limit: 10 }),
      ]);
      if (seq !== this.seq) return;
      await this.renderAnswer(answer, hits);
    } catch (e) {
      if (seq !== this.seq) return;
      p.markDisconnected((e as Error).message);
      this.renderOffline(q);
    }
  }

  private async renderAnswer(a: Answer, hits: Hit[]): Promise<void> {
    const el = this.resultsEl;
    el.empty();
    const pct = Math.round(a.confidence * 100);
    if (a.confident && a.answer && a.path) {
      this.setStatus(`FAQ answer, ${pct}% match`, "ok");
      const card = el.createDiv({ cls: "okvault-answer" });
      card.setAttr("data-source", this.plugin.toObsidian(a.path));
      const head = card.createDiv({ cls: "okvault-answer-head" });
      const icon = head.createSpan({ cls: "okvault-answer-icon" });
      setIcon(icon, "badge-check");
      const q = head.createEl("a", { text: a.matched_question ?? a.path, cls: "okvault-answer-q" });
      q.addEventListener("click", act((e) => this.plugin.openNote(a.path!, e.ctrlKey || e.metaKey)));
      head.createSpan({ text: `${pct}%`, cls: "okvault-badge", attr: { title: "kb_ask confidence" } });
      const body = card.createDiv({ cls: "okvault-answer-body markdown-rendered" });
      await MarkdownRenderer.render(this.app, a.answer, body, this.plugin.toObsidian(a.path), this);
      const foot = card.createDiv({ cls: "okvault-answer-foot" });
      foot.createSpan({ text: a.path, cls: "okvault-path" });
    } else {
      this.setStatus(
        `No FAQ matches confidently${pct ? ` (best ${pct}%)` : ""}. Agents get the notes below.`,
        "warn",
      );
      if (a.related?.length) this.renderHits(el, "Related notes", a.related, true);
    }
    const shown = new Set([a.path, ...(a.related ?? []).map((h) => h.path)]);
    const more = hits.filter((h) => !shown.has(h.path));
    if (more.length) this.renderHits(el, "Search results", more, true);
    if (!el.childElementCount) el.createDiv({ cls: "okvault-empty", text: "Nothing in the vault matches." });
  }

  private renderHits(el: HTMLElement, title: string, hits: Hit[], showSnippet: boolean): void {
    if (!hits.length) return;
    const sec = el.createDiv({ cls: "okvault-section" });
    sec.createDiv({ cls: "okvault-section-title", text: title });
    for (const h of hits) {
      const row = sec.createDiv({ cls: "okvault-hit" });
      row.addEventListener("click", act((e) => this.plugin.openNote(h.path, e.ctrlKey || e.metaKey)));
      const top = row.createDiv({ cls: "okvault-hit-top" });
      top.createSpan({ text: h.title || h.path, cls: "okvault-hit-title" });
      if (h.type) top.createSpan({ text: h.type, cls: "okvault-type" });
      const text = showSnippet && h.snippet ? h.snippet : h.description;
      if (text) row.createDiv({ cls: "okvault-hit-desc", text: stripMarks(text) });
      row.createDiv({ cls: "okvault-path", text: h.path });
    }
  }

  /** Without a server: FAQ titles and aliases from Obsidian's metadata cache. */
  private renderOffline(q: string): void {
    const p = this.plugin;
    this.setStatus(`okvault is not reachable (${p.lastError || p.settings.serverUrl}). Showing FAQ title matches only.`, "err");
    const el = this.resultsEl;
    el.empty();
    const words = q.toLowerCase().split(/\W+/).filter((w) => w.length > 2);
    const hits: Hit[] = [];
    for (const n of p.localNotes()) {
      if (n.type.toLowerCase() !== "faq") continue;
      const hay = [n.title, ...n.aliases].join(" ").toLowerCase();
      const score = words.filter((w) => hay.includes(w)).length;
      if (words.length && !score) continue;
      hits.push({ path: n.path, title: n.title, type: n.type, description: n.description, score });
    }
    hits.sort((a, b) => b.score - a.score || a.title.localeCompare(b.title));
    this.renderHits(el, q ? "FAQ title matches" : "All FAQs", hits.slice(0, 50), false);
    const retry = el.createEl("button", { text: "Reconnect", cls: "okvault-retry" });
    retry.addEventListener("click", act(async () => {
      await p.connect(true);
      await this.run();
    }));
  }

  private setStatus(text: string, kind: "ok" | "warn" | "err"): void {
    this.statusEl.setText(text);
    this.statusEl.className = `okvault-status okvault-status-${kind}`;
  }
}

/** Bleve highlights matches with <mark>; show plain text. */
function stripMarks(s: string): string {
  return s.replace(/<\/?mark>/g, "");
}
