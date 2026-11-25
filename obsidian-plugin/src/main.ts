import { FileSystemAdapter, MarkdownView, Notice, Plugin, TFile, WorkspaceLeaf, debounce, parseYaml, requestUrl } from "obsidian";
import { GitStatus, OkvaultClient, Post } from "./client";
import { NoteInput } from "./bundle";
import { BUNDLE_VIEW, BundleView } from "./bundleView";
import { FAQ_VIEW, FaqView } from "./faqView";
import { Finding, OkvaultProcess, lintVault, syncNow, verifyNote } from "./server";
import { INSPECTOR_VIEW, InspectorView } from "./inspectorView";
import { TICKET_VIEW, TicketView } from "./ticketView";
import { GAPS_VIEW, GapsView } from "./gapsView";
import { TICKET_KEY, isProjectKey, normalizeKey, ticketKeyAt } from "./noteFacts";
import { TEAM_VIEW, TeamView } from "./teamView";
import { DEFAULT_SETTINGS, OkvaultSettingTab, OkvaultSettings } from "./settings";
import { IGNORE_FILE, VaultIgnore, normalizeRoot } from "./paths";

export interface LocalNote extends NoteInput {
  aliases: string[];
  description: string;
}

/** requestUrl is not subject to CORS, which okvault's HTTP handler does not send. */
const obsidianPost: Post = async (url, headers, body) => {
  const r = await requestUrl({ url, method: "POST", headers, body, contentType: headers["Content-Type"], throw: false });
  return { status: r.status, headers: r.headers, text: r.text };
};

type State = "unknown" | "connected" | "starting" | "offline";

/** The vault's git policy from .okvault.yaml (what okvault will do, whether or not it runs). */
export interface TeamGit {
  mode: string; // off | commit | push | pr
  base?: string;
  commitEdits?: string;
  pullInterval?: string;
  error?: string;
}

/** Obsidian Git settings that make it commit, pull or push on its own. */
const OBSIDIAN_GIT_AUTO: { key: string; label: string }[] = [
  { key: "autoSaveInterval", label: "Auto commit-and-sync interval" },
  { key: "autoBackupAfterFileChange", label: "Auto commit-and-sync after stopping file edits" },
  { key: "autoPushInterval", label: "Auto push interval" },
  { key: "autoPullInterval", label: "Auto pull interval" },
  { key: "autoPullOnBoot", label: "Pull on startup" },
];

export default class OkvaultPlugin extends Plugin {
  declare settings: OkvaultSettings;
  client!: OkvaultClient;
  lastError = "";
  private state: State = "unknown";
  private proc = new OkvaultProcess();
  private connecting: Promise<boolean> | null = null;
  private statusBar!: HTMLElement;
  /** Last git sync state okvault reported (null: git off, read-only server, or unknown). */
  git: GitStatus | null = null;
  teamGit: TeamGit = { mode: "off" };
  syncing = false;
  private lastProblem = "";
  /** Ticket project keys from .okvault.yaml (tracker.projects); empty: keys are not linked in notes. */
  ticketProjects: string[] = [];
  /** okvault lint results for the whole vault, by note path. */
  lint: { findings: Map<string, Finding[]>; error: string; at: number; running: boolean } = {
    findings: new Map(),
    error: "",
    at: 0,
    running: false,
  };
  private lintAgain = false;
  /** Rules from the okvault root's .vaultignore; ignored files are left out of every view. */
  ignore = new VaultIgnore();
  private ignoreText = "";

  override async onload(): Promise<void> {
    await this.loadSettings();
    this.client = new OkvaultClient(this.settings.serverUrl, obsidianPost);

    this.registerView(FAQ_VIEW, (leaf) => new FaqView(leaf, this));
    this.registerView(BUNDLE_VIEW, (leaf) => new BundleView(leaf, this));
    this.registerView(TEAM_VIEW, (leaf) => new TeamView(leaf, this));
    this.registerView(INSPECTOR_VIEW, (leaf) => new InspectorView(leaf, this));
    this.registerView(TICKET_VIEW, (leaf) => new TicketView(leaf, this));
    this.registerView(GAPS_VIEW, (leaf) => new GapsView(leaf, this));

    this.addRibbonIcon("message-circle-question", "okvault: ask the FAQ", () => this.openFaq());
    this.addRibbonIcon("orbit", "okvault: bundle map", () => this.openBundle());
    this.addRibbonIcon("git-pull-request", "okvault: versioning and review", () => this.openTeam());
    this.addRibbonIcon("shield-check", "okvault: note inspector", () => this.openInspector());
    this.addRibbonIcon("ticket", "okvault: ticket workspace", () => this.openTicket());
    this.addRibbonIcon("circle-help", "okvault: knowledge gaps", () => this.openGaps());

    this.addCommand({ id: "ask", name: "Ask the FAQ", callback: () => this.openFaq() });
    this.addCommand({
      id: "ask-selection",
      name: "Ask the FAQ about the selection",
      editorCallback: (editor) => {
        const sel = editor.getSelection().trim();
        this.openFaq(sel || undefined);
      },
    });
    this.addCommand({ id: "bundle", name: "Open the bundle map", callback: () => this.openBundle() });
    this.addCommand({ id: "team", name: "Open versioning and review", callback: () => this.openTeam() });
    this.addCommand({ id: "inspector", name: "Open the note inspector", callback: () => this.openInspector() });
    this.addCommand({ id: "tickets", name: "Open the ticket workspace", callback: () => this.openTicket() });
    this.addCommand({ id: "gaps", name: "Open knowledge gaps (questions no FAQ answers)", callback: () => this.openGaps() });
    this.addCommand({
      id: "ticket-at-cursor",
      name: "Open the ticket under the cursor",
      editorCheckCallback: (checking, editor) => {
        const cur = editor.getCursor();
        const sel = editor.getSelection().trim();
        const key = (sel && normalizeKey(sel)) || ticketKeyAt(editor.getLine(cur.line), cur.ch, this.ticketProjects);
        if (!key) return false;
        if (!checking) void this.openTicket(key);
        return true;
      },
    });

    // Ticket keys of the team's projects become links to the ticket workspace in reading view.
    this.registerMarkdownPostProcessor((el) => {
      if (!this.ticketProjects.length) return;
      const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
      const hits: Text[] = [];
      for (let n = walker.nextNode(); n; n = walker.nextNode()) {
        const parent = n.parentElement;
        if (parent?.closest("a, code, pre")) continue;
        TICKET_KEY.lastIndex = 0;
        if (TICKET_KEY.test(n.textContent ?? "")) hits.push(n as Text);
      }
      for (const t of hits) this.linkTickets(t);
    });

    const lintSoon = debounce(() => this.requestLint(), 3000, true);
    for (const ev of ["modify", "create", "delete", "rename"] as const) {
      this.registerEvent(
        (this.app.vault.on as (e: string, cb: () => void) => ReturnType<typeof this.app.vault.on>)(ev, () => {
          if (this.app.workspace.getLeavesOfType(INSPECTOR_VIEW).length) lintSoon();
        }),
      );
    }
    this.addCommand({ id: "sync", name: "Sync now (commit edits, rebase, push, update the pull request)", callback: () => this.sync() });
    this.addCommand({
      id: "verify",
      name: "Mark this note as checked",
      checkCallback: (checking) => {
        const f = this.app.workspace.getActiveFile();
        const p = f ? this.fromObsidian(f.path) : null;
        if (!f || !p || f.extension !== "md") return false;
        if (!checking) void this.verify(p);
        return true;
      },
    });
    this.addCommand({
      id: "reconnect",
      name: "Reconnect to the server (start it if needed)",
      callback: async () => {
        const ok = await this.connect(true);
        new Notice(ok ? `okvault connected (${this.settings.serverUrl})` : `okvault: ${this.lastError}`);
        this.refreshViews();
      },
    });

    this.addSettingTab(new OkvaultSettingTab(this.app, this));
    this.statusBar = this.addStatusBarItem();
    this.statusBar.addClass("okvault-statusbar");
    this.statusBar.addEventListener("click", () => this.openTeam());
    this.updateStatusBar();

    this.app.workspace.onLayoutReady(async () => {
      await this.loadIgnore();
      await this.loadTeamGit();
      await this.connect(false);
      await this.refreshGit();
    });
    // okvault syncs in the background; show what it last reported.
    // Obsidian sends no events for dotfiles it does not index, so .vaultignore is re-read here too.
    this.registerInterval(
      window.setInterval(() => {
        void this.refreshGit();
        void this.loadIgnore().then((changed) => changed && this.refreshViews());
      }, 30_000),
    );
    this.registerEvent(
      this.app.vault.on("modify", (f) => {
        if (f.path === this.toObsidian(".okvault.yaml")) void this.loadTeamGit().then(() => this.refreshViews());
        if (f.path === this.toObsidian(IGNORE_FILE)) void this.loadIgnore().then(() => this.refreshViews());
      }),
    );
  }

  override onunload(): void {
    this.proc.stop();
  }

  async loadSettings(): Promise<void> {
    this.settings = Object.assign({}, DEFAULT_SETTINGS, await this.loadData());
    // Older versions saved "." or "/" as is; those mean the vault root.
    this.settings.vaultFolder = normalizeRoot(this.settings.vaultFolder) ?? "";
  }

  /** Re-reads .vaultignore at the okvault root; true when its rules changed. */
  async loadIgnore(): Promise<boolean> {
    let text = "";
    try {
      const p = this.toObsidian(IGNORE_FILE);
      if (await this.app.vault.adapter.exists(p)) text = await this.app.vault.adapter.read(p);
    } catch {
      text = "";
    }
    if (text === this.ignoreText) return false;
    this.ignoreText = text;
    this.ignore = new VaultIgnore(text);
    return true;
  }

  async saveSettings(): Promise<void> {
    const prev = (await this.loadData()) as Partial<OkvaultSettings> | null;
    await this.saveData(this.settings);
    // A server this plugin started runs with the old flags: restart it.
    const flagsChanged =
      prev?.versioning !== this.settings.versioning ||
      prev?.binaryPath !== this.settings.binaryPath ||
      prev?.vaultFolder !== this.settings.vaultFolder;
    if (prev?.vaultFolder !== this.settings.vaultFolder) {
      // .vaultignore lives at the okvault root, which just moved.
      void this.loadIgnore().then(() => this.refreshViews());
    }
    if (this.proc.running && flagsChanged) {
      this.proc.stop();
      this.proc.exitInfo = "";
      this.state = "unknown";
      this.git = null;
      void this.connect(true).then(() => this.refreshGit());
    }
    this.client?.setUrl(this.settings.serverUrl);
    this.state = "unknown";
    this.updateStatusBar();
  }

  /** Connected, or try once to connect (and start okvault when enabled). */
  async ensureConnected(): Promise<boolean> {
    if (this.state === "connected") return true;
    return this.connect(false);
  }

  /** Probe the server; start okvault if nothing answers and auto-start is on. */
  connect(force: boolean): Promise<boolean> {
    if (!this.connecting) {
      this.connecting = this.doConnect(force).finally(() => (this.connecting = null));
    }
    return this.connecting;
  }

  private async doConnect(force: boolean): Promise<boolean> {
    if (!force && this.state === "offline" && !this.settings.autoStart) return false;
    this.client.setUrl(this.settings.serverUrl);
    if (await this.client.ping()) return this.checkVault();
    if (!this.settings.autoStart) {
      this.lastError = `nothing answers at ${this.settings.serverUrl}`;
      return this.setState("offline");
    }
    if (!force && this.proc.exitInfo) {
      // We already tried to start it and it died; don't respawn on every keystroke.
      this.lastError = this.proc.exitInfo;
      return this.setState("offline");
    }
    let url: URL;
    try {
      url = new URL(this.settings.serverUrl);
    } catch {
      this.lastError = `bad server URL ${this.settings.serverUrl}`;
      return this.setState("offline");
    }
    if (!["127.0.0.1", "localhost", "[::1]"].includes(url.hostname)) {
      this.lastError = `nothing answers at ${url.host}, and okvault is only started for a local URL`;
      return this.setState("offline");
    }
    const adapter = this.app.vault.adapter;
    if (!(adapter instanceof FileSystemAdapter)) {
      this.lastError = "this vault is not on the local file system";
      return this.setState("offline");
    }
    this.setState("starting");
    this.proc.start({
      binary: this.settings.binaryPath,
      vaultDir: this.vaultDir()!,
      versioning: this.settings.versioning === "follow",
      host: url.hostname === "localhost" ? "127.0.0.1" : url.hostname.replace(/^\[|\]$/g, ""),
      port: Number(url.port) || 80,
    });
    for (let i = 0; i < 40; i++) {
      await sleep(250);
      if (await this.client.ping()) return this.checkVault();
      if (!this.proc.running) break;
    }
    this.lastError = this.proc.exitInfo || `okvault did not answer at ${url.host}: ${this.proc.lastLog()}`;
    this.proc.stop();
    return this.setState("offline");
  }

  /** Connected only if the server serves this vault: some of its note paths exist here. */
  private async checkVault(): Promise<boolean> {
    try {
      const paths = await this.client.paths();
      const local = new Set(this.localNotes().map((n) => n.path));
      if (paths.length && !paths.some((p) => local.has(p))) {
        this.lastError = `the server at ${this.settings.serverUrl} serves a different vault (check the URL or the okvault root setting)`;
        return this.setState("offline");
      }
    } catch (e) {
      this.lastError = (e as Error).message;
      return this.setState("offline");
    }
    return this.setState("connected");
  }

  // ---- versioning and collaboration (okvault's git sync) ----

  /** Absolute path of the okvault vault, or null when the vault is not on disk. */
  vaultDir(): string | null {
    const adapter = this.app.vault.adapter;
    if (!(adapter instanceof FileSystemAdapter)) return null;
    const base = adapter.getBasePath();
    return this.settings.vaultFolder ? `${base}/${this.settings.vaultFolder}` : base;
  }

  /** Reads git policy from .okvault.yaml (Obsidian does not index dotfiles, so read it directly). */
  async loadTeamGit(): Promise<void> {
    const p = this.toObsidian(".okvault.yaml");
    try {
      if (!(await this.app.vault.adapter.exists(p))) {
        this.teamGit = { mode: "off" };
        return;
      }
      const y = (parseYaml(await this.app.vault.adapter.read(p)) ?? {}) as { git?: Record<string, unknown> };
      const g = y.git ?? {};
      this.teamGit = {
        mode: String(g.mode ?? "off").toLowerCase(),
        base: g.base ? String(g.base) : undefined,
        commitEdits: g.commit_edits ? String(g.commit_edits) : undefined,
        pullInterval: g.pull_interval !== undefined ? String(g.pull_interval) : undefined,
      };
      const t = ((y as { tracker?: { projects?: unknown } }).tracker?.projects ?? []) as unknown;
      this.ticketProjects = (Array.isArray(t) ? t : String(t).split(/[,\s]+/)).map((x) => String(x).trim().toUpperCase()).filter(Boolean);
    } catch (e) {
      this.teamGit = { mode: "off", error: `.okvault.yaml: ${(e as Error).message}` };
    }
  }

  /** Asks okvault for its sync state (kb_review carries it); a new problem raises a notice once. */
  async refreshGit(): Promise<void> {
    if (this.state !== "connected") return;
    try {
      const r = await this.client.review(1);
      this.setGit(r.git ?? null);
    } catch (e) {
      this.markDisconnected((e as Error).message);
    }
    this.refreshTeamView();
  }

  private setGit(g: GitStatus | null): void {
    this.git = g;
    const problem = g?.problem ?? "";
    if (problem && problem !== this.lastProblem) new Notice(`okvault sync: ${problem}`, 15_000);
    this.lastProblem = problem;
    this.updateStatusBar();
  }

  /** `okvault sync`: commit edits made here, rebase, push, update the pull request. */
  async sync(): Promise<void> {
    const dir = this.vaultDir();
    if (!dir) return void new Notice("okvault: this vault is not on the local file system");
    if (this.teamGit.mode === "off") {
      return void new Notice("okvault: git is off for this vault. Set git.mode in .okvault.yaml (commit, push or pr).");
    }
    if (this.syncing) return;
    this.syncing = true;
    this.refreshTeamView();
    const notice = new Notice("okvault: syncing…", 0);
    try {
      const r = await syncNow(this.settings.binaryPath, dir);
      notice.hide();
      if (r.error) {
        new Notice(`okvault sync: ${r.error}`, 15_000);
      } else if (r.status) {
        this.setGit(r.status);
        const pr = r.status.pull_request ? ` · ${r.status.pull_request}` : "";
        if (!r.status.problem) new Notice(`okvault: synced (${r.status.mode}${r.status.branch ? ` on ${r.status.branch}` : ""})${pr}`);
      }
    } finally {
      notice.hide();
      this.syncing = false;
      await this.refreshGit();
      this.refreshTeamView();
    }
  }

  /** `okvault verify`: a person records that they checked the note. Only ever on an explicit click. */
  async verify(okPath: string): Promise<boolean> {
    const dir = this.vaultDir();
    if (!dir) return false;
    const r = await verifyNote(this.settings.binaryPath, dir, okPath);
    new Notice(`okvault: ${r.message}`, r.ok ? 5_000 : 15_000);
    await this.refreshGit();
    this.refreshViews();
    return r.ok;
  }

  /** Obsidian Git's automatic commit/pull/push, which competes with okvault for the same checkout. */
  obsidianGitConflicts(): { enabled: boolean; auto: string[]; pushEnabled: boolean } {
    // Internal API: guard every step, it may change between Obsidian versions.
    const plugins = (this.app as unknown as {
      plugins?: { enabledPlugins?: Set<string>; plugins?: Record<string, { settings?: Record<string, unknown> }> };
    }).plugins;
    const enabled = !!plugins?.enabledPlugins?.has("obsidian-git");
    if (!enabled) return { enabled, auto: [], pushEnabled: false };
    const st = plugins?.plugins?.["obsidian-git"]?.settings ?? {};
    const auto = OBSIDIAN_GIT_AUTO.filter((o) => {
      const v = st[o.key];
      return typeof v === "number" ? v > 0 : v === true;
    }).map((o) => o.label);
    return { enabled, auto, pushEnabled: st.disablePush !== true };
  }

  openObsidianGitSettings(): void {
    const setting = (this.app as unknown as { setting?: { open(): void; openTabById(id: string): void } }).setting;
    setting?.open();
    setting?.openTabById("obsidian-git");
  }

  markDisconnected(reason: string): void {
    this.lastError = reason;
    this.setState("offline");
  }

  private setState(s: State): boolean {
    this.state = s;
    if (s === "connected") this.lastError = "";
    this.updateStatusBar();
    return s === "connected";
  }

  statusText(): string {
    switch (this.state) {
      case "connected":
        return `Connected to okvault ${this.client.serverVersion} at ${this.settings.serverUrl}${this.proc.running ? " (started by this plugin)" : ""}.`;
      case "starting":
        return "Starting okvault…";
      case "offline":
        return `Not connected: ${this.lastError}`;
      default:
        return "Not connected yet.";
    }
  }

  private updateStatusBar(): void {
    if (!this.statusBar) return;
    let label = { connected: "okvault ●", starting: "okvault …", offline: "okvault ○", unknown: "okvault ○" }[this.state];
    const g = this.git;
    if (this.state === "connected" && g) {
      label += ` ${g.mode}`;
      if (g.unpushed) label += ` ↑${g.unpushed}`;
      if (g.problem) label += " ⚠";
    }
    this.statusBar.setText(label);
    this.statusBar.setAttr("aria-label", this.statusText() + (g?.problem ? `\nSync problem: ${g.problem}` : ""));
    this.statusBar.toggleClass("is-offline", this.state === "offline");
    this.statusBar.toggleClass("has-problem", !!g?.problem);
  }

  // ---- paths: okvault paths are relative to the okvault root ----

  toObsidian(p: string): string {
    return this.settings.vaultFolder ? `${this.settings.vaultFolder}/${p}` : p;
  }

  fromObsidian(p: string): string | null {
    const f = this.settings.vaultFolder;
    if (!f) return p;
    return p.startsWith(f + "/") ? p.slice(f.length + 1) : null;
  }

  async openNote(okPath: string, newLeaf: boolean): Promise<void> {
    const p = this.toObsidian(okPath);
    const file = this.app.vault.getAbstractFileByPath(p);
    if (!(file instanceof TFile)) {
      new Notice(`okvault: ${p} is not in this vault. Check the "okvault root" setting.`);
      return;
    }
    const leaf = newLeaf ? this.app.workspace.getLeaf("tab") : this.mainLeaf();
    await leaf.openFile(file);
  }

  /** A leaf in the main area, so opening a note does not replace a sidebar view. */
  private mainLeaf(): WorkspaceLeaf {
    const md = this.app.workspace.getActiveViewOfType(MarkdownView);
    if (md) return md.leaf;
    return this.app.workspace.getLeaf(false);
  }

  /** The vault's notes as Obsidian's metadata cache sees them (live, no server needed). */
  localNotes(): LocalNote[] {
    const mc = this.app.metadataCache;
    const out: LocalNote[] = [];
    for (const file of this.app.vault.getMarkdownFiles()) {
      const p = this.fromObsidian(file.path);
      if (p === null || this.ignore.match(p)) continue;
      const fm = mc.getFileCache(file)?.frontmatter ?? {};
      const links: string[] = [];
      for (const target of Object.keys(mc.resolvedLinks[file.path] ?? {})) {
        const t = this.fromObsidian(target);
        if (t !== null && t.endsWith(".md") && !this.ignore.match(t)) links.push(t);
      }
      out.push({
        path: p,
        title: str(fm.title) || file.basename,
        type: str(fm.type),
        status: str(fm.status),
        aliases: list(fm.aliases),
        description: str(fm.description),
        links,
        broken: Object.keys(mc.unresolvedLinks[file.path] ?? {}).length,
      });
    }
    return out;
  }

  // ---- views ----

  async openFaq(query?: string): Promise<void> {
    const leaf = await this.revealView(FAQ_VIEW, "right");
    const view = leaf.view as FaqView;
    if (query) view.setQuery(query);
    else view.focus();
  }

  async openGaps(): Promise<void> {
    await this.revealView(GAPS_VIEW, "right");
  }

  async openInspector(): Promise<void> {
    await this.revealView(INSPECTOR_VIEW, "right");
  }

  /** Opens the ticket workspace, on a ticket when given. */
  async openTicket(key?: string): Promise<void> {
    const leaf = await this.revealView(TICKET_VIEW, "right");
    const v = leaf.view as TicketView;
    if (key) await v.show(key);
    else v.focus();
  }

  /** Wraps ticket keys in a text node with links to the ticket workspace. */
  private linkTickets(t: Text): void {
    const text = t.textContent ?? "";
    const frag = createFragment();
    let last = 0;
    for (const m of text.matchAll(TICKET_KEY)) {
      const i = m.index ?? 0;
      if (!isProjectKey(m[0], this.ticketProjects)) continue;
      frag.appendText(text.slice(last, i));
      const a = frag.createEl("a", { cls: "okvault-ticket-key", text: m[0], attr: { title: `Open ${m[0]} in the ticket workspace` } });
      a.addEventListener("click", (e) => {
        e.preventDefault();
        void this.openTicket(m[0]);
      });
      last = i + m[0].length;
    }
    if (!last) return;
    frag.appendText(text.slice(last));
    t.replaceWith(frag);
  }

  /** Runs okvault lint for the whole vault (one at a time), then refreshes the inspector. */
  requestLint(): void {
    if (this.lint.running) {
      this.lintAgain = true;
      return;
    }
    const dir = this.vaultDir();
    if (!dir) return;
    this.lint.running = true;
    void lintVault(this.settings.binaryPath, dir).then((r) => {
      this.lint = { findings: r.findings, error: r.error ?? "", at: Date.now(), running: false };
      for (const l of this.app.workspace.getLeavesOfType(INSPECTOR_VIEW)) (l.view as InspectorView).refresh();
      if (this.lintAgain) {
        this.lintAgain = false;
        this.requestLint();
      }
    });
  }

  async openTeam(): Promise<void> {
    await this.revealView(TEAM_VIEW, "right");
  }

  private refreshTeamView(): void {
    for (const l of this.app.workspace.getLeavesOfType(TEAM_VIEW)) (l.view as TeamView).render();
  }

  async openBundle(): Promise<void> {
    await this.revealView(BUNDLE_VIEW, "main");
  }

  private async revealView(type: string, where: "right" | "main"): Promise<WorkspaceLeaf> {
    const ws = this.app.workspace;
    let leaf = ws.getLeavesOfType(type)[0];
    if (!leaf) {
      leaf = where === "right" ? (ws.getRightLeaf(false) ?? ws.getLeaf("tab")) : ws.getLeaf("tab");
      await leaf.setViewState({ type, active: true });
    }
    await ws.revealLeaf(leaf);
    return leaf;
  }

  refreshViews(): void {
    for (const l of this.app.workspace.getLeavesOfType(GAPS_VIEW)) void (l.view as GapsView).reload();
    for (const l of this.app.workspace.getLeavesOfType(INSPECTOR_VIEW)) (l.view as InspectorView).refresh();
    for (const l of this.app.workspace.getLeavesOfType(TEAM_VIEW)) void (l.view as TeamView).reload();
    for (const l of this.app.workspace.getLeavesOfType(FAQ_VIEW)) (l.view as FaqView).refresh();
    for (const l of this.app.workspace.getLeavesOfType(BUNDLE_VIEW)) void (l.view as BundleView).reload();
  }
}

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

function list(v: unknown): string[] {
  if (Array.isArray(v)) return v.filter((x): x is string => typeof x === "string");
  return typeof v === "string" ? [v] : [];
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => window.setTimeout(r, ms));
}
