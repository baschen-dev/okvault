import { ItemView, Notice, TFile, WorkspaceLeaf, debounce, setIcon } from "obsidian";
import type { Gap, GapsResult } from "./client";
import type OkvaultPlugin from "./main";
import { folderFor, mergeAliases, notePath, relativeLink } from "./noteFacts";
import { createNote, templateFor } from "./notes";

export const GAPS_VIEW = "okvault-gaps";

/** From this confidence on, the nearest FAQ probably answers the question and only needs the phrasing as an alias. */
const NEAR_MISS = 0.5;

/**
 * Knowledge gaps: what agents asked that no FAQ answers, most asked first
 * (kb_gaps). Answer it with a new FAQ, fix a near miss with aliases, or dismiss it.
 */
export class GapsView extends ItemView {
  private result: GapsResult | null = null;
  private error = "";
  private loading = false;
  private busy = new Set<string>();

  private reloadSoon = debounce(() => void this.reload(), 1500, true);

  constructor(
    leaf: WorkspaceLeaf,
    private plugin: OkvaultPlugin,
  ) {
    super(leaf);
  }

  getViewType(): string {
    return GAPS_VIEW;
  }
  getDisplayText(): string {
    return "okvault gaps";
  }
  override getIcon(): string {
    return "circle-help";
  }

  override async onOpen(): Promise<void> {
    this.contentEl.addClass("okvault-gaps");
    // A new or edited FAQ can close a gap: okvault reindexes, then we re-ask.
    this.registerEvent(
      this.app.metadataCache.on("changed", (f) => {
        if (String(this.app.metadataCache.getFileCache(f)?.frontmatter?.type ?? "").toLowerCase() === "faq") this.reloadSoon();
      }),
    );
    await this.reload();
  }

  async reload(): Promise<void> {
    this.loading = true;
    this.render();
    this.error = "";
    if (!(await this.plugin.ensureConnected())) {
      this.error = `okvault is not reachable (${this.plugin.lastError}).`;
    } else {
      try {
        this.result = await this.plugin.client.gaps({ limit: 50 });
      } catch (e) {
        const m = (e as Error).message;
        this.error = /unknown tool|kb_gaps/i.test(m)
          ? "This okvault does not record knowledge gaps: it needs a newer okvault, or it runs with --gaps off."
          : m;
      }
    }
    this.loading = false;
    this.render();
  }

  private render(): void {
    const el = this.contentEl;
    el.empty();
    const head = el.createDiv({ cls: "okvault-toolbar" });
    const r = this.result;
    head.createDiv({
      cls: "okvault-info",
      text: r ? `${r.total} open · ${r.answered} answered since · ${r.dismissed} dismissed` : this.loading ? "Loading…" : "",
    });
    const btn = head.createEl("button", { cls: "clickable-icon", attr: { "aria-label": "Refresh" } });
    setIcon(btn, "refresh-cw");
    btn.addEventListener("click", () => this.reload());
    el.createDiv({
      cls: "okvault-hint",
      text: "Questions agents asked that no FAQ answers confidently, most asked first. Recorded on this machine only; your own searches in the FAQ panel are not counted.",
    });
    if (this.error) el.createDiv({ cls: "okvault-status okvault-status-err", text: this.error });
    if (!r) return;
    if (!r.gaps.length) {
      el.createDiv({
        cls: "okvault-empty",
        text: r.answered || r.dismissed ? "No open gaps: every question agents asked has an FAQ answer now." : "No gaps recorded yet. They appear as agents ask questions the FAQ cannot answer.",
      });
    }
    for (const g of r.gaps) this.renderGap(el, g);
    if (r.total > r.gaps.length) el.createDiv({ cls: "okvault-muted", text: `${r.total - r.gaps.length} more not shown.` });
    el.createDiv({ cls: "okvault-path okvault-gaps-log", text: `log: ${r.log}` });
  }

  private renderGap(el: HTMLElement, g: Gap): void {
    const card = el.createDiv({ cls: "okvault-card okvault-gap" });
    const top = card.createDiv({ cls: "okvault-card-head" });
    top.createDiv({ cls: "okvault-gap-q", text: g.question });
    top.createSpan({ cls: "okvault-badge", text: `${g.asked}×`, attr: { title: `asked ${g.asked} time${g.asked > 1 ? "s" : ""}` } });
    const who = g.clients?.length ? ` by ${g.clients.join(", ")}` : "";
    card.createDiv({ cls: "okvault-hit-desc", text: `last asked ${ago(g.last_asked)}${who}` });

    const others = g.phrasings.slice(1);
    if (others.length) {
      const d = card.createDiv({ cls: "okvault-gap-also" });
      d.createSpan({ cls: "okvault-muted", text: "Also asked as: " });
      d.createSpan({ text: others.map((p) => `“${p}”`).join(", ") });
    }

    const near = g.nearest_faq;
    const nearFile = near ? this.app.vault.getAbstractFileByPath(this.plugin.toObsidian(near.path)) : null;
    const nearIsDraft = nearFile instanceof TFile && String(this.app.metadataCache.getFileCache(nearFile)?.frontmatter?.status ?? "").toLowerCase() === "draft";
    if (near && nearIsDraft) {
      const n = card.createDiv({ cls: "okvault-gap-near okvault-gap-draft" });
      n.createSpan({ text: "Draft answer in progress: " });
      this.link(n, near.question, near.path);
      n.createDiv({ cls: "okvault-hint", text: "Write the answer, then remove status: draft so kb_ask gives it to agents." });
    } else if (near && near.confidence >= NEAR_MISS) {
      const n = card.createDiv({ cls: "okvault-gap-near" });
      n.createSpan({ text: "Near miss: " });
      this.link(n, near.question, near.path);
      n.createSpan({ cls: "okvault-muted", text: ` (${Math.round(near.confidence * 100)}%)` });
      n.createDiv({ cls: "okvault-hint", text: "If that FAQ answers it, adding these phrasings as its aliases makes kb_ask answer." });
    }

    if (g.related?.length) {
      const rel = card.createDiv({ cls: "okvault-gap-related" });
      rel.createSpan({ cls: "okvault-muted", text: "Agents read instead: " });
      g.related.forEach((h, i) => {
        if (i) rel.appendText(" · ");
        this.link(rel, h.title || h.path, h.path);
      });
    }

    const actions = card.createDiv({ cls: "okvault-gap-actions" });
    const busy = this.busy.has(g.id);
    if (!nearIsDraft) {
      const ans = actions.createEl("button", { text: "Answer this", cls: "mod-cta" });
      ans.disabled = busy;
      ans.setAttr("title", "Create an FAQ for this question (a draft until you write the answer), with the other phrasings as aliases");
      ans.addEventListener("click", () => this.run(g.id, () => this.answer(g)));
    }
    if (near && (nearIsDraft || near.confidence >= NEAR_MISS)) {
      const al = actions.createEl("button", { text: nearIsDraft ? "Add phrasings to the draft" : "Add as aliases" });
      al.disabled = busy;
      al.addEventListener("click", () => this.run(g.id, () => this.addAliases(near.path, g.phrasings)));
    }
    const dis = actions.createEl("button", { text: "Dismiss" });
    dis.disabled = busy;
    dis.setAttr("title", "Not a question the vault should answer; hide it on this machine");
    dis.addEventListener("click", () =>
      this.run(g.id, async () => {
        this.result = await this.plugin.client.gaps({ dismiss: g.id, limit: 50 });
      }),
    );
  }

  private link(el: HTMLElement, text: string, okPath: string): void {
    const a = el.createEl("a", { text, attr: { title: okPath } });
    a.addEventListener("click", (e) => this.plugin.openNote(okPath, e.ctrlKey || e.metaKey));
  }

  private async run(id: string, fn: () => Promise<unknown>): Promise<void> {
    this.busy.add(id);
    this.render();
    try {
      await fn();
    } catch (e) {
      new Notice(`okvault: ${(e as Error).message}`, 10_000);
    }
    this.busy.delete(id);
    this.render();
  }

  /** A draft FAQ: the question as title, the other phrasings as aliases, links to what agents read instead. */
  private async answer(g: Gap): Promise<void> {
    const folder = folderFor("FAQ", this.plugin.localNotes());
    const title = capitalize(g.question.replace(/\s*\?+\s*$/, ""));
    const rel = notePath(folder, title) ?? "";
    const links = (g.related ?? []).map((h) => `- [${h.title || h.path}](${relativeLink(rel, h.path)})`);
    const r = await createNote(this.plugin, {
      template: templateFor(this.plugin, "FAQ"),
      title,
      folder,
      body: links.length ? `Related:\n\n${links.join("\n")}` : undefined,
      edit: (fm) => {
        fm.aliases = mergeAliases(fm.aliases, g.phrasings, title);
        fm.status = "draft";
      },
    });
    if (!r.file) throw new Error(`${r.error}; open it and add the phrasings as aliases instead`);
    await this.app.workspace.getLeaf("tab").openFile(r.file);
    new Notice(`okvault: created ${r.rel} as a draft. Write the answer, then remove status: draft so agents get it.`, 10_000);
  }

  private async addAliases(okPath: string, phrasings: string[]): Promise<void> {
    const f = this.app.vault.getAbstractFileByPath(this.plugin.toObsidian(okPath));
    if (!(f instanceof TFile)) throw new Error(`${okPath} is not in this vault`);
    await this.app.fileManager.processFrontMatter(f, (fm) => {
      fm.aliases = mergeAliases(fm.aliases, phrasings, String(fm.title ?? ""));
    });
    new Notice(`okvault: added ${phrasings.length === 1 ? "the phrasing" : "the phrasings"} to ${okPath}`);
    this.reloadSoon();
  }
}

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function ago(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const s = Math.round((Date.now() - t) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  if (s < 86400) return `${Math.round(s / 3600)} h ago`;
  return `${Math.round(s / 86400)} days ago`;
}
