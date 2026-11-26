import { ItemView, WorkspaceLeaf, setIcon } from "obsidian";
import type { ReviewItem } from "./client";
import type OkvaultPlugin from "./main";
import { act } from "./act";

export const TEAM_VIEW = "okvault-team";

const REASON_LABEL: Record<string, string> = {
  "broken-links": "⛔ broken links",
  stale: "⏰ stale",
  unverified: "⚠ agent edit, not checked",
  draft: "✎ draft",
  "no-owner": "👤 no owner",
};

/** Versioning and collaboration: okvault's git sync state, and the notes that need a person. */
export class TeamView extends ItemView {
  private items: ReviewItem[] = [];
  private total = 0;
  private reviewError = "";
  private busy = new Set<string>();

  constructor(
    leaf: WorkspaceLeaf,
    private plugin: OkvaultPlugin,
  ) {
    super(leaf);
  }

  getViewType(): string {
    return TEAM_VIEW;
  }
  getDisplayText(): string {
    return "okvault versioning";
  }
  override getIcon(): string {
    return "git-pull-request";
  }

  override async onOpen(): Promise<void> {
    this.contentEl.addClass("okvault-team");
    await this.plugin.loadTeamGit();
    await this.reload();
  }

  /** Fetch the review queue and sync state, then draw. */
  async reload(): Promise<void> {
    this.reviewError = "";
    if (await this.plugin.ensureConnected()) {
      try {
        const r = await this.plugin.client.review(100);
        this.items = r.items;
        this.total = r.total;
      } catch (e) {
        this.reviewError = (e as Error).message;
      }
      await this.plugin.refreshGit();
    } else {
      this.reviewError = `okvault is not reachable: ${this.plugin.lastError}`;
    }
    this.render();
  }

  render(): void {
    const root = this.contentEl;
    root.empty();
    this.renderSync(root.createDiv({ cls: "okvault-card" }));
    this.renderObsidianGit(root);
    this.renderReview(root.createDiv());
  }

  private renderSync(el: HTMLElement): void {
    const p = this.plugin;
    const t = p.teamGit;
    const g = p.git;
    const head = el.createDiv({ cls: "okvault-card-head" });
    head.createSpan({ cls: "okvault-card-title", text: "Versioning" });
    const btn = head.createEl("button", { text: p.syncing ? "Syncing…" : "Sync now", cls: "mod-cta" });
    btn.disabled = p.syncing || t.mode === "off";
    btn.setAttr("title", "Commit edits made here, rebase on the remote, push and update the pull request (okvault sync)");
    btn.addEventListener("click", act(() => p.sync()));

    if (t.error) el.createDiv({ cls: "okvault-problem", text: t.error });
    if (t.mode === "off") {
      el.createDiv({ cls: "okvault-muted", text: "Git is off for this vault: okvault does not commit or sync. Turn it on for the team with git.mode in .okvault.yaml (commit, push or pr)." });
      return;
    }

    const policy = el.createDiv({ cls: "okvault-kv" });
    const row = (k: string, v: string | HTMLElement) => {
      policy.createSpan({ cls: "okvault-k", text: k });
      const cell = policy.createSpan({ cls: "okvault-v" });
      if (typeof v === "string") cell.setText(v);
      else cell.appendChild(v);
    };
    const modeText: Record<string, string> = {
      commit: "commit: each change is a local commit",
      push: "push: commits are rebased and pushed",
      pr: `pr: changes go to a pull request into ${t.base ?? "the default branch"}`,
    };
    row("Team policy", modeText[t.mode] ?? t.mode);
    row("Your edits", t.commitEdits ? `committed after ${t.commitEdits} of quiet` : "committed only when you press Sync now");
    if (t.mode !== "commit") row("Background sync", t.pullInterval === "0" ? "after writes only" : `every ${t.pullInterval ?? "5m"}`);

    if (g) {
      if (g.branch) row("Branch", g.base ? `${g.branch} → ${g.base}` : g.branch);
      if (g.pull_request) {
        const a = createEl("a", { text: prLabel(g.pull_request), href: g.pull_request });
        row("Pull request", a);
      }
      row("Not pushed", String(g.unpushed ?? 0));
      if (g.last_sync) row("Last sync", ago(g.last_sync));
      if (g.problem) el.createDiv({ cls: "okvault-problem", text: g.problem });
    } else if (p.settings.versioning === "off") {
      el.createDiv({ cls: "okvault-muted", text: "Versioning is off in this plugin's settings, so the server it started does not sync. Sync now still works." });
    } else if (p.lastError) {
      el.createDiv({ cls: "okvault-muted", text: `okvault is not reachable (${p.lastError}). Sync now still works.` });
    } else {
      el.createDiv({ cls: "okvault-muted", text: "The server does not report git sync (it may be read-only, or run by someone else). Sync now still works." });
    }
    if (!t.commitEdits) {
      el.createDiv({
        cls: "okvault-hint",
        text: "commit_edits is not set in .okvault.yaml, so nothing commits edits made in Obsidian until you press Sync now (or an agent writes).",
      });
    }
  }

  /** Obsidian Git and okvault both committing the same checkout: say which one should. */
  private renderObsidianGit(root: HTMLElement): void {
    const p = this.plugin;
    if (p.teamGit.mode === "off") return;
    const og = p.obsidianGitConflicts();
    if (!og.enabled) return;
    const auto = og.auto.length > 0;
    const prPush = p.teamGit.mode === "pr" && og.pushEnabled;
    if (!auto && !prPush) return;
    const el = root.createDiv({ cls: "okvault-card okvault-warn" });
    el.createDiv({ cls: "okvault-card-title", text: "Obsidian Git is also syncing this vault" });
    el.createDiv({
      text: "okvault commits, rebases and pushes this vault. Obsidian Git doing the same in the same checkout races it for git's lock, replaces okvault's commits with “vault backup” commits and merge commits, and can push past review. Let okvault write to git and keep Obsidian Git for history and diffs.",
    });
    const ul = el.createEl("ul");
    for (const a of og.auto) ul.createEl("li", { text: `Turn off: ${a}` });
    if (prPush) ul.createEl("li", { text: "Turn on “Disable push”: okvault works on a personal branch with no upstream, so Obsidian Git's push fails on every sync" });
    const b = el.createEl("button", { text: "Open Obsidian Git settings" });
    b.addEventListener("click", () => p.openObsidianGitSettings());
  }

  private renderReview(el: HTMLElement): void {
    const head = el.createDiv({ cls: "okvault-section-title okvault-section-head" });
    const label = head.createSpan({ text: `Needs a person (${this.total})` });
    label.setAttr("title", "kb_review: stale, agent edits nobody checked yet, drafts, broken links, no owner");
    const rescan = head.createEl("button", {
      cls: "clickable-icon",
      attr: { "aria-label": "Rescan the vault (okvault re-reads every note: broken links, review status)" },
    });
    setIcon(rescan, "refresh-cw");
    rescan.addEventListener("click", act(async () => {
      rescan.disabled = true;
      await this.plugin.rescan();
      rescan.disabled = false;
    }));
    if (this.reviewError) {
      el.createDiv({ cls: "okvault-muted", text: this.reviewError });
      return;
    }
    if (!this.items.length) {
      el.createDiv({ cls: "okvault-muted", text: "Nothing to review." });
      return;
    }
    for (const it of this.items) {
      const row = el.createDiv({ cls: "okvault-hit okvault-review" });
      const top = row.createDiv({ cls: "okvault-hit-top" });
      const title = top.createSpan({ cls: "okvault-hit-title", text: it.title || it.path });
      title.addEventListener("click", act((e) => this.plugin.openNote(it.path, e.ctrlKey || e.metaKey)));
      if (it.type) top.createSpan({ text: it.type, cls: "okvault-type" });
      const reasons = row.createDiv({ cls: "okvault-reasons" });
      for (const r of it.reasons) reasons.createSpan({ cls: `okvault-reason okvault-reason-${r}`, text: REASON_LABEL[r] ?? r });
      const who: string[] = [];
      if (it.generated) who.push(`written by ${it.generated.by} ${day(it.generated.at)}`);
      if (it.last_verified) who.push(`checked by ${it.last_verified.by} ${day(it.last_verified.at)}`);
      if (it.owners?.length) who.push(`owners: ${it.owners.join(", ")}`);
      if (it.detail) who.push(it.detail);
      if (who.length) row.createDiv({ cls: "okvault-hit-desc", text: who.join(" · ") });
      // Verifying is a person's statement: only on this explicit click, and only where it means something.
      if (it.reasons.some((r) => r === "unverified" || r === "stale")) {
        const b = row.createEl("button", { text: this.busy.has(it.path) ? "Recording…" : "Mark as checked", cls: "okvault-verify" });
        b.disabled = this.busy.has(it.path);
        b.setAttr("title", "okvault verify: records that you read this note and it is correct");
        b.addEventListener("click", act(async (e) => {
          e.stopPropagation();
          this.busy.add(it.path);
          this.render();
          await this.plugin.verify(it.path);
          this.busy.delete(it.path);
          await this.reload();
        }));
      }
    }
  }
}

function prLabel(url: string): string {
  const m = /\/pull\/(\d+)/.exec(url);
  return m ? `#${m[1]}` : url.includes("/compare/") ? "open one on GitHub" : url;
}

function ago(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const s = Math.round((Date.now() - t) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  if (s < 86400) return `${Math.round(s / 3600)} h ago`;
  return new Date(t).toLocaleString();
}

function day(iso: string): string {
  const t = Date.parse(iso);
  return Number.isNaN(t) ? iso : new Date(t).toLocaleDateString();
}
