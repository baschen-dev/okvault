import { Notice, Setting, TFolder } from "obsidian";
import type OkvaultPlugin from "./main";
import { checkConfig } from "./server";
import {
  CONFIG_FILE,
  applyTeamConfig,
  checkTeamConfig,
  readTeamConfig,
  splitList,
  type GitMode,
  type TeamConfig,
  type TrackerKind,
} from "./teamConfig";

const MODES: Record<GitMode, string> = {
  off: "Off: okvault never runs git",
  commit: "Commit: each change is a local commit",
  push: "Push: commit, rebase and push to the branch",
  pr: "Pull request: work on a personal branch, one pull request",
};

/**
 * The team settings editor: a form over .okvault.yaml. Edits go to a draft;
 * Save checks it (here, then with okvault itself) and writes it, keeping the
 * file's comments and every key this form does not show.
 */
export class TeamSettingsSection {
  private src = ""; // file text the draft started from
  private exists = false;
  private draft: TeamConfig | null = null;
  private parseError = "";
  private errorEls = new Map<string, HTMLElement>();
  private statusEl!: HTMLElement;
  private previewEl!: HTMLElement;
  private saveBtn!: HTMLButtonElement;
  private saving = false;
  private root: HTMLElement | null = null;

  constructor(private plugin: OkvaultPlugin) {}

  private get path(): string {
    return this.plugin.toObsidian(CONFIG_FILE);
  }

  /** Load the file (again) and draw the form into el. */
  async display(el: HTMLElement): Promise<void> {
    this.root = el;
    await this.load();
    this.render(el);
  }

  /** The tracker in the draft, for the connections helper; null when the file cannot be edited. */
  tracker(): TeamConfig["tracker"] | null {
    return this.draft && !this.parseError ? this.draft.tracker : null;
  }

  /**
   * Puts a Jira site and projects into the draft (from the connections helper)
   * and redraws. Nothing is written: the person reviews and saves as usual.
   */
  applyJira(url: string, projects: string[]): boolean {
    if (!this.draft || this.parseError || !this.root) return false;
    const t = this.draft.tracker;
    if (t.url.replace(/\/+$/, "") !== url.replace(/\/+$/, "")) t.api = ""; // a new site: let okvault pick the API version
    t.kind = "jira";
    t.url = url.replace(/\/+$/, "");
    t.projects = projects;
    this.render(this.root);
    this.root.querySelector(".okvault-team-foot")?.scrollIntoView({ block: "nearest" });
    return true;
  }

  private async load(): Promise<void> {
    const a = this.plugin.app.vault.adapter;
    this.exists = await a.exists(this.path);
    this.src = this.exists ? await a.read(this.path) : "";
    const r = readTeamConfig(this.src);
    this.draft = r.config;
    this.parseError = r.error ?? "";
  }

  private render(el: HTMLElement): void {
    el.empty();
    this.errorEls.clear();
    new Setting(el).setName("Team settings (.okvault.yaml)").setHeading();
    el.createDiv({
      cls: "setting-item-description okvault-team-intro",
      text:
        "Shared by everyone who uses this vault and committed with it; okvault picks changes up while it runs. " +
        "Never put secrets here: Jira and GitHub credentials come from each person's environment. " +
        "Everyone should run the same okvault version, because okvault rejects keys it does not know.",
    });
    if (this.parseError) {
      el.createDiv({ cls: "okvault-problem", text: `${CONFIG_FILE} is not valid YAML, so it cannot be edited here: ${this.parseError}` });
      return;
    }
    const c = this.draft!;

    // ---- git ----
    new Setting(el).setName("Versioning (git)").setHeading();
    new Setting(el)
      .setName("Git mode")
      .setDesc("How okvault records changes in the vault's git repository, for the whole team. One machine can still override it with okvault serve --git.")
      .addDropdown((d) => {
        for (const [k, v] of Object.entries(MODES)) d.addOption(k, v);
        d.setValue(c.git.mode).onChange((v) => {
          c.git.mode = v as GitMode;
          this.render(el);
        });
      });
    if (c.git.mode !== "off") {
      this.text(el, "git.commit_edits", "Commit edits made in Obsidian",
        "After the vault has been quiet this long, e.g. 10m. Empty: only okvault's own writes, verify and Sync now are committed.",
        "never", () => c.git.commit_edits, (v) => (c.git.commit_edits = v));
    }
    if (c.git.mode === "push" || c.git.mode === "pr") {
      this.text(el, "git.pull_interval", "Background sync",
        "How often a running okvault fetches, rebases and pushes, e.g. 5m. 0: only after okvault's own writes.",
        "5m", () => c.git.pull_interval, (v) => (c.git.pull_interval = v));
    }
    if (c.git.mode === "pr") {
      this.text(el, "git.base", "Pull requests target", "The branch pull requests merge into.", "the remote's default branch",
        () => c.git.base, (v) => (c.git.base = v));
      this.text(el, "git.branch", "Working branch",
        "Leave empty so each person gets their own branch, okvault/<git user.name>. A fixed name is shared by everyone.",
        "okvault/<your git name>", () => c.git.branch, (v) => (c.git.branch = v));
    }
    if (c.git.mode !== "off") {
      this.text(el, "git.remote", "Remote", "The git remote okvault fetches from and pushes to.", "origin",
        () => c.git.remote, (v) => (c.git.remote = v));
    }

    // ---- review ----
    new Setting(el).setName("Review").setHeading();
    this.text(el, "review_interval", "Review interval",
      "How long a person's check (okvault verify, Mark as checked) keeps a note fresh, e.g. 180d, 26w.",
      "180d", () => c.review_interval, (v) => (c.review_interval = v));

    // ---- owners ----
    new Setting(el)
      .setName("Folder owners")
      .setDesc("Default owners of the notes in a folder (people or groups). A note's own owners win; the longest matching folder applies; . is the whole vault.")
      .addButton((b) =>
        b.setButtonText("Add folder").onClick(() => {
          c.owners.push({ folder: "", owners: [] });
          this.render(el);
        }),
      );
    const folders = this.folders();
    const listId = "okvault-folder-list";
    const dl = el.createEl("datalist", { attr: { id: listId } });
    for (const f of folders) dl.createEl("option", { attr: { value: f } });
    c.owners.forEach((row, i) => {
      const s = new Setting(el).setClass("okvault-owner-row");
      s.addText((t) => {
        t.setPlaceholder("folder, e.g. ops").setValue(row.folder).onChange((v) => {
          row.folder = v;
          this.changed();
        });
        t.inputEl.setAttr("list", listId);
      });
      s.addText((t) =>
        t
          .setPlaceholder("owners, e.g. team-ops, alice")
          .setValue(row.owners.join(", "))
          .onChange((v) => {
            row.owners = splitList(v);
            this.changed();
          }),
      );
      s.addExtraButton((b) =>
        b.setIcon("trash").setTooltip("Remove").onClick(() => {
          c.owners.splice(i, 1);
          this.render(el);
        }),
      );
      this.errorEls.set(`owners.${i}`, s.descEl.createDiv({ cls: "okvault-field-error" }));
    });

    // ---- tracker ----
    new Setting(el).setName("Ticket tracker").setHeading();
    new Setting(el)
      .setName("Tracker")
      .setDesc("Where tickets live. okvault only reads from it. Credentials: JIRA_API_TOKEN (and JIRA_EMAIL on Jira Cloud) in each person's environment.")
      .addDropdown((d) =>
        d
          .addOption("", "None")
          .addOption("none", "Links only (no fetching)")
          .addOption("jira", "Jira")
          .setValue(c.tracker.kind)
          .onChange((v) => {
            c.tracker.kind = v as TrackerKind;
            this.render(el);
          }),
      );
    if (c.tracker.kind) {
      this.text(el, "tracker.url", "URL", "e.g. https://acme.atlassian.net", "https://…",
        () => c.tracker.url, (v) => (c.tracker.url = v));
      this.text(el, "tracker.projects", "Project keys",
        "Keys okvault recognises in notes, e.g. PROJ, OPS. Comma separated.", "PROJ, OPS",
        () => c.tracker.projects.join(", "), (v) => (c.tracker.projects = splitList(v)));
      if (c.tracker.kind === "jira") {
        new Setting(el)
          .setName("Jira API version")
          .setDesc("Automatic: 3 on *.atlassian.net, else 2 (Data Center).")
          .addDropdown((d) =>
            d
              .addOption("", "Automatic")
              .addOption("3", "3 (Cloud)")
              .addOption("2", "2 (Data Center)")
              .setValue(c.tracker.api)
              .onChange((v) => {
                c.tracker.api = v as "" | "2" | "3";
                this.changed();
              }),
          );
      }
      this.text(el, "tracker.browse", "Ticket link",
        "Optional link template with {key}; default is the URL + /browse/{key}.", "https://…/browse/{key}",
        () => c.tracker.browse, (v) => (c.tracker.browse = v));
    }

    // ---- save ----
    const foot = el.createDiv({ cls: "okvault-team-foot" });
    this.statusEl = foot.createDiv({ cls: "okvault-team-status" });
    const buttons = foot.createDiv({ cls: "okvault-team-buttons" });
    const revert = buttons.createEl("button", { text: "Revert" });
    revert.addEventListener("click", async () => {
      await this.load();
      this.render(el);
    });
    this.saveBtn = buttons.createEl("button", { text: "Save", cls: "mod-cta" });
    this.saveBtn.addEventListener("click", () => this.save(el));
    const details = el.createEl("details", { cls: "okvault-preview" });
    details.createEl("summary", { text: `Preview ${CONFIG_FILE}` });
    this.previewEl = details.createEl("pre");
    this.changed();
  }

  /** A text setting bound to a field, with its own error line. */
  private text(el: HTMLElement, key: string, name: string, desc: string, placeholder: string, get: () => string, set: (v: string) => void): void {
    const s = new Setting(el).setName(name).setDesc(desc);
    s.addText((t) =>
      t
        .setPlaceholder(placeholder)
        .setValue(get())
        .onChange((v) => {
          set(v.trim());
          this.changed();
        }),
    );
    this.errorEls.set(key, s.descEl.createDiv({ cls: "okvault-field-error" }));
  }

  /** After any edit: field errors, preview, status, Save enabled. */
  private changed(): void {
    const c = this.draft!;
    const errs = checkTeamConfig(c);
    for (const [k, e] of this.errorEls) {
      const msg = errs.get(k) ?? "";
      e.setText(msg);
      e.closest(".okvault-owner-row")?.toggleClass("okvault-has-error", msg !== "");
    }
    let out = this.src;
    let dirty = false;
    try {
      out = applyTeamConfig(this.src, c);
      dirty = out !== this.src;
    } catch (e) {
      errs.set("file", (e as Error).message);
    }
    this.previewEl.setText(out || "(empty)");
    this.saveBtn.disabled = !dirty || errs.size > 0 || this.saving;
    if (errs.size) this.status(`Fix ${errs.size} problem${errs.size > 1 ? "s" : ""} to save.`, "err");
    else if (dirty) this.status("Unsaved changes.", "warn");
    else this.status(this.exists ? "Saved." : `No ${CONFIG_FILE} yet: okvault uses its defaults.`, "ok");
  }

  private status(text: string, kind: "ok" | "warn" | "err"): void {
    this.statusEl.setText(text);
    this.statusEl.className = `okvault-team-status okvault-status-${kind}`;
  }

  private async save(el: HTMLElement): Promise<void> {
    const p = this.plugin;
    const a = p.app.vault.adapter;
    const current = (await a.exists(this.path)) ? await a.read(this.path) : "";
    if (current !== this.src) {
      this.status(`${CONFIG_FILE} changed on disk since you opened these settings (a teammate's change, synced?). Revert to load it, then make your change again.`, "err");
      return;
    }
    const out = applyTeamConfig(this.src, this.draft!);
    this.saving = true;
    this.saveBtn.disabled = true;
    this.status("Checking with okvault…", "warn");
    const check = await checkConfig(p.settings.binaryPath, out);
    this.saving = false;
    if (check.errors.length) {
      this.status(`okvault rejects this: ${check.errors.join("; ")}`, "err");
      this.saveBtn.disabled = false;
      return;
    }
    await a.write(this.path, out);
    await p.loadTeamGit();
    p.refreshViews();
    const commit = p.teamGit.mode !== "off" && p.teamGit.commitEdits ? " okvault commits it with your next edits." : " Commit it so your teammates get it.";
    new Notice(`Saved ${CONFIG_FILE}.${commit}${check.unavailable ? ` (okvault could not check it: ${check.unavailable})` : ""}`, 8000);
    await this.load();
    this.render(el);
  }

  /** Vault folders, for the owners folder suggestions. */
  private folders(): string[] {
    const out = ["."];
    for (const f of this.plugin.app.vault.getAllLoadedFiles()) {
      if (!(f instanceof TFolder) || f.isRoot()) continue;
      const p = this.plugin.fromObsidian(f.path);
      if (p && !p.split("/").some((s) => s.startsWith(".") || s.startsWith("_"))) out.push(p);
    }
    return out.sort();
  }
}
