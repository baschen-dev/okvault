import { Notice, Setting, setIcon } from "obsidian";
import type OkvaultPlugin from "./main";
import { Connections, checkConnections } from "./server";
import type { TeamSettingsSection } from "./teamSettings";

/**
 * Connections (this machine): what okvault can reach with this machine's
 * credentials, and a picker for the team's Jira projects. The plugin stores no
 * secrets: okvault finds them itself (Jira in JIRA_API_TOKEN / JIRA_EMAIL,
 * GitHub in GH_TOKEN, GITHUB_TOKEN or the GitHub CLI's login, git in its
 * credential manager or SSH agent) and reports only where they came from.
 */
export class ConnectionsSection {
  private result: Connections | null = null;
  private error = "";
  private checking = false;
  private jiraUrl = "";
  private selected: Set<string> | null = null;
  private filter = "";
  private root: HTMLElement | null = null;
  private onSelect: () => void = () => {};

  constructor(
    private plugin: OkvaultPlugin,
    private team: TeamSettingsSection,
  ) {}

  display(el: HTMLElement): void {
    this.root = el;
    if (!this.jiraUrl) this.jiraUrl = this.team.tracker()?.url ?? "";
    this.render();
  }

  private render(): void {
    const el = this.root;
    if (!el) return;
    el.empty();
    new Setting(el).setName("Connections (this machine)").setHeading();
    el.createDiv({
      cls: "setting-item-description okvault-team-intro",
      text:
        "What okvault can reach with this machine's credentials. The plugin stores none: okvault reads Jira from JIRA_API_TOKEN (and JIRA_EMAIL on Jira Cloud), " +
        "GitHub from GH_TOKEN, GITHUB_TOKEN or the GitHub CLI's login, and pushes with git's own credential manager or SSH key. " +
        "Obsidian reads environment variables when it starts, so restart it after setting one. Claude Code's okvault sees Claude Code's environment, which can differ.",
    });

    const s = new Setting(el)
      .setName("Jira site")
      .setDesc("The team's Jira, e.g. https://acme.atlassian.net. Leave empty to check GitHub only.")
      .addText((t) =>
        t
          .setPlaceholder("https://acme.atlassian.net")
          .setValue(this.jiraUrl)
          .onChange((v) => {
            this.jiraUrl = v.trim();
          }),
      )
      .addButton((b) => {
        b.setButtonText(this.checking ? "Checking…" : "Check connections")
          .setCta()
          .onClick(() => this.check());
        b.buttonEl.disabled = this.checking;
      });
    s.settingEl.addClass("okvault-conn-check");

    if (this.error) el.createDiv({ cls: "okvault-problem", text: this.error });
    const r = this.result;
    if (!r) return;
    this.renderJira(el, r);
    this.renderGitHub(el, r);
  }

  private async check(): Promise<void> {
    const dir = this.plugin.vaultDir();
    if (!dir) {
      this.error = "This vault is not on the local file system.";
      return this.render();
    }
    this.checking = true;
    this.error = "";
    this.render();
    const r = await checkConnections(this.plugin.settings.binaryPath, dir, {
      jiraUrl: this.jiraUrl || undefined,
      jiraApi: this.jiraUrl && this.jiraUrl === this.team.tracker()?.url ? this.team.tracker()?.api || undefined : undefined,
    });
    this.checking = false;
    this.result = r.result ?? null;
    this.error = r.error ?? "";
    this.selected = null; // start from the vault's current projects
    this.render();
  }

  // ---- Jira ----

  private renderJira(el: HTMLElement, r: Connections): void {
    const card = el.createDiv({ cls: "okvault-card okvault-conn" });
    const j = r.jira;
    this.cardHead(card, "Jira", !!j && !j.problem, !j);
    if (!j) {
      card.createDiv({ cls: "okvault-muted", text: "No Jira site to check: enter one above, or set the tracker in Team settings." });
      return;
    }
    this.row(card, "Site", `${j.url} (REST API ${j.api})`);
    this.row(card, "Credentials", j.credentials);
    if (j.user) this.row(card, "Signed in as", j.user);
    if (j.problem) card.createDiv({ cls: "okvault-problem", text: j.problem });
    if (j.credentials === "none" || /sign|401|403/i.test(j.problem ?? "")) {
      const help = card.createDiv({ cls: "okvault-hint" });
      help.appendText("Create an API token at ");
      help.createEl("a", { text: "id.atlassian.com → Security → API tokens", href: "https://id.atlassian.com/manage-profile/security/api-tokens" });
      help.appendText(
        " (Data Center: a personal access token). Set it as JIRA_API_TOKEN, and your Atlassian email as JIRA_EMAIL on Jira Cloud, in your user environment (Windows: setx JIRA_API_TOKEN …; macOS: launchctl setenv; Linux: your session profile). Then restart Obsidian and check again.",
      );
    }
    if (j.projects?.length) this.renderProjects(card, j.url, j.projects, !!j.truncated);
  }

  private renderProjects(card: HTMLElement, url: string, projects: { key: string; name: string }[], truncated: boolean): void {
    const current = this.team.tracker()?.projects ?? [];
    if (!this.selected) this.selected = new Set(current.map((k) => k.toUpperCase()));
    const sel = this.selected;
    const visible = new Set(projects.map((p) => p.key));
    const missing = [...sel].filter((k) => !visible.has(k));

    const box = card.createDiv({ cls: "okvault-projects" });
    const head = box.createDiv({ cls: "okvault-projects-head" });
    const count = head.createSpan({ cls: "okvault-k" });
    const filter = head.createEl("input", { type: "search", attr: { placeholder: "Filter" } });
    filter.value = this.filter;
    const list = box.createDiv({ cls: "okvault-project-list" });
    const draw = () => {
      list.empty();
      const f = this.filter.toLowerCase();
      for (const k of missing) this.projectRow(list, k, "not visible to you (archived, or no access)", true);
      for (const p of projects) {
        if (f && !p.key.toLowerCase().includes(f) && !p.name.toLowerCase().includes(f)) continue;
        this.projectRow(list, p.key, p.name, false);
      }
    };
    filter.addEventListener("input", () => {
      this.filter = filter.value;
      draw();
    });
    draw();

    const foot = box.createDiv({ cls: "okvault-projects-foot" });
    const use = foot.createEl("button", { text: "Use for this vault", cls: "mod-cta" });
    use.setAttr("title", "Puts this Jira site and the selected projects into Team settings below; review and Save there");
    use.addEventListener("click", () => {
      const keys = [...sel].sort();
      if (this.team.applyJira(url, keys)) new Notice("okvault: Jira site and projects set in Team settings below. Review them and press Save.");
      else new Notice("okvault: .okvault.yaml cannot be edited here (see Team settings).");
    });
    const note = foot.createSpan({ cls: "okvault-muted" });
    this.onSelect = () => {
      count.setText(`Projects okvault recognises in notes (${sel.size} selected of ${projects.length}${truncated ? "+" : ""})`);
      const same = current.length === sel.size && current.every((k) => sel.has(k.toUpperCase())) && (this.team.tracker()?.url ?? "") === url;
      note.setText(same ? "Matches Team settings." : "");
    };
    this.onSelect();
  }

  private projectRow(list: HTMLElement, key: string, name: string, missing: boolean): void {
    const sel = this.selected!;
    const label = list.createEl("label", { cls: `okvault-project${missing ? " is-missing" : ""}` });
    const cb = label.createEl("input", { type: "checkbox" });
    cb.checked = sel.has(key);
    cb.addEventListener("change", () => {
      if (cb.checked) sel.add(key);
      else sel.delete(key);
      this.onSelect();
    });
    label.createSpan({ cls: "okvault-project-key", text: key });
    label.createSpan({ cls: "okvault-muted", text: name });
  }

  // ---- GitHub and git ----

  private renderGitHub(el: HTMLElement, r: Connections): void {
    const g = r.github;
    const card = el.createDiv({ cls: "okvault-card okvault-conn" });
    const prNeedsApi = r.git_mode === "pr";
    const ok = !g.problem && (g.push === "ok" || !g.push) && (!g.api_problem || !prNeedsApi);
    this.cardHead(card, "GitHub and git", ok, false);
    this.row(card, "Team git mode", r.git_mode === "off" ? "off (okvault does not commit or push)" : r.git_mode);
    if (g.problem) {
      card.createDiv({ cls: "okvault-problem", text: g.problem });
      return;
    }
    this.row(card, "Remote", `${g.remote_name}: ${g.remote ?? ""}`);
    if (g.push) this.row(card, "git push", g.push === "ok" ? "works (checked with a dry run; nothing was sent)" : g.push, g.push !== "ok");
    if (g.repo) {
      this.row(card, "Repository", g.repo);
      this.row(card, "API token", g.token);
      if (g.user) this.row(card, "Signed in as", `${g.user}${g.can_push === undefined ? "" : g.can_push ? " (can push)" : " (read only)"}`);
    }
    if (g.api_problem) {
      card.createDiv({
        cls: prNeedsApi ? "okvault-problem" : "okvault-hint",
        text: prNeedsApi ? g.api_problem : `${g.api_problem}. Only pr mode needs it.`,
      });
    }
    if (g.token === "none" || (g.push && g.push !== "ok")) {
      const help = card.createDiv({ cls: "okvault-hint" });
      help.appendText("Run ");
      help.createEl("code", { text: "gh auth login" });
      help.appendText(" in a terminal (okvault uses the GitHub CLI's token for pull requests), then ");
      help.createEl("code", { text: "gh auth setup-git" });
      help.appendText(" so git pushes with it too. Or set GH_TOKEN in your environment and restart Obsidian.");
    }
  }

  private cardHead(card: HTMLElement, title: string, ok: boolean, neutral: boolean): void {
    const h = card.createDiv({ cls: "okvault-card-head" });
    h.createSpan({ cls: "okvault-card-title", text: title });
    const s = h.createSpan({ cls: `okvault-conn-state ${neutral ? "" : ok ? "is-ok" : "is-bad"}` });
    setIcon(s, neutral ? "minus-circle" : ok ? "check-circle" : "alert-circle");
    s.appendText(neutral ? " not set" : ok ? " connected" : " needs attention");
  }

  private row(card: HTMLElement, k: string, v: string, bad = false): void {
    const r = card.createDiv({ cls: "okvault-kv" });
    r.createSpan({ cls: "okvault-k", text: k });
    r.createSpan({ cls: `okvault-v${bad ? " okvault-t-error" : ""}`, text: v });
  }
}
