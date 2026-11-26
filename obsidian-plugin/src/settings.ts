import { App, PluginSettingTab, Setting, SettingPage, requireApiVersion, type SettingDefinitionItem } from "obsidian";
import type OkvaultPlugin from "./main";
import { TeamSettingsSection } from "./teamSettings";
import { ConnectionsSection } from "./connectionsSection";
import { DEFAULT_SETTINGS, cleanSetting, rootProblem, type EditableKey, type OkvaultSettings } from "./settingsModel.ts";

export { DEFAULT_SETTINGS, type OkvaultSettings };

const TEXT = {
  heading: "This plugin, on this machine",
  serverUrl: {
    name: "Server URL",
    desc: "okvault's MCP endpoint. Run `okvault serve --vault <this vault> --port 8766`, or let the plugin start it. The default is 8766, not okvault's 8765, so a server the plugin starts never takes the port you gave Claude Code; point this at that server instead to share it.",
  },
  autoStart: {
    name: "Start okvault automatically",
    desc: "When nothing answers at the server URL, run `okvault serve` on its port for this vault (with the versioning below), and stop it when Obsidian closes.",
  },
  versioning: {
    name: "Versioning",
    desc: "For the server this plugin starts. Follow .okvault.yaml: okvault keeps index.md files current as you edit and, when git.mode is set, commits your edits, rebases, pushes and opens the pull request, as the team configured. Off: read-only; nothing is written or committed. A server you run yourself keeps its own flags.",
    options: { follow: "Follow .okvault.yaml", off: "Off (read-only)" },
  },
  binaryPath: {
    name: "okvault binary",
    desc: "The full path of the okvault program you downloaded, for example C:\\Users\\you\\okvault\\okvault.exe or /Users/you/okvault/okvault. `okvault` alone works if it is on your PATH (go install puts it in $GOPATH/bin).",
  },
  vaultFolder: {
    name: "okvault root",
    desc: "The okvault folder, relative to this Obsidian vault. Empty, `.` or `/` (the default) mean the vault itself, the usual setup when you open `vault/` as the Obsidian vault. If you opened the repository root instead, enter `vault`. Notes outside it are left out, and its `.okvault.yaml` and `.vaultignore` are the ones used.",
  },
  team: {
    name: "Team settings and connections",
    desc: "The vault's shared .okvault.yaml (git sync and pull requests, review interval, folder owners, ticket tracker), checked by okvault before it is saved, and what this machine's Jira and GitHub credentials reach.",
  },
};

/**
 * On Obsidian 1.13 and later the tab is declarative (getSettingDefinitions), so
 * its settings show up in the settings search; the team form and the
 * connections check, which depend on each other, are one sub-page drawn by
 * their own code. Older versions call display(), which draws the same tab by hand.
 */
export class OkvaultSettingTab extends PluginSettingTab {
  private team: TeamSettingsSection;
  private connections: ConnectionsSection;
  constructor(
    app: App,
    private plugin: OkvaultPlugin,
  ) {
    super(app, plugin);
    this.team = new TeamSettingsSection(plugin);
    this.connections = new ConnectionsSection(plugin, this.team);
  }

  private folderExists = (p: string): boolean => !!this.app.vault.getAbstractFileByPath(p);

  override getSettingDefinitions(): SettingDefinitionItem[] {
    return [
      {
        type: "group",
        heading: TEXT.heading,
        items: [
          { ...TEXT.serverUrl, control: { type: "text", key: "serverUrl", placeholder: DEFAULT_SETTINGS.serverUrl } },
          { ...TEXT.autoStart, control: { type: "toggle", key: "autoStart" } },
          { name: TEXT.versioning.name, desc: TEXT.versioning.desc, control: { type: "dropdown", key: "versioning", options: TEXT.versioning.options } },
          { ...TEXT.binaryPath, control: { type: "text", key: "binaryPath", placeholder: "okvault" } },
          {
            ...TEXT.vaultFolder,
            control: {
              type: "folder",
              key: "vaultFolder",
              placeholder: "/ (vault root)",
              includeRoot: true,
              validate: (v: string) => rootProblem(v, this.folderExists) || undefined,
            },
          },
          {
            name: "Connection",
            aliases: ["reconnect", "server status"],
            render: (s: Setting) => {
              s.setName("Connection").setDesc(this.plugin.statusText()).addButton((b) =>
                b.setButtonText("Reconnect").onClick(async () => {
                  await this.plugin.connect(true);
                  s.setDesc(this.plugin.statusText());
                }),
              );
            },
          },
          { type: "page", ...TEXT.team, page: () => this.teamPage() },
        ],
      },
    ];
  }

  override getControlValue(key: string): unknown {
    return this.plugin.settings[key as EditableKey];
  }

  override async setControlValue(key: string, value: unknown): Promise<void> {
    const v = cleanSetting(key, value);
    if (v === undefined || v === this.plugin.settings[key as EditableKey]) return;
    (this.plugin.settings as unknown as Record<string, unknown>)[key] = v;
    await this.plugin.saveSettings();
  }

  /** Built on demand: SettingPage only exists from Obsidian 1.13, which is also the only version that asks for it. */
  private teamPage(): SettingPage {
    if (!requireApiVersion("1.13.0")) throw new Error("okvault: the team settings page needs Obsidian 1.13 or later");
    const team = this.team;
    const connections = this.connections;
    const Page = class extends SettingPage {
      override display(): void {
        this.containerEl.empty();
        drawTeamAndConnections(this.containerEl, team, connections);
      }
    };
    const page = new Page();
    page.title = TEXT.team.name;
    return page;
  }

  /** Obsidian before 1.13. */
  override display(): void {
    const { containerEl } = this;
    containerEl.empty();
    const s = this.plugin.settings;
    const save = async (key: EditableKey, v: unknown) => this.setControlValue(key, v);
    new Setting(containerEl).setName(TEXT.heading).setHeading();

    new Setting(containerEl)
      .setName(TEXT.serverUrl.name)
      .setDesc(TEXT.serverUrl.desc)
      .addText((t) => t.setPlaceholder(DEFAULT_SETTINGS.serverUrl).setValue(s.serverUrl).onChange((v) => save("serverUrl", v)));

    new Setting(containerEl)
      .setName(TEXT.autoStart.name)
      .setDesc(TEXT.autoStart.desc)
      .addToggle((t) => t.setValue(s.autoStart).onChange((v) => save("autoStart", v)));

    new Setting(containerEl)
      .setName(TEXT.versioning.name)
      .setDesc(TEXT.versioning.desc)
      .addDropdown((d) => d.addOptions(TEXT.versioning.options).setValue(s.versioning).onChange((v) => save("versioning", v)));

    new Setting(containerEl)
      .setName(TEXT.binaryPath.name)
      .setDesc(TEXT.binaryPath.desc)
      .addText((t) => t.setPlaceholder("okvault").setValue(s.binaryPath).onChange((v) => save("binaryPath", v)));

    const root = new Setting(containerEl).setName(TEXT.vaultFolder.name).setDesc(TEXT.vaultFolder.desc);
    const problem = root.descEl.createDiv({ cls: "okvault-setting-problem" });
    root.addText((t) => {
      t.setPlaceholder("/ (vault root)")
        .setValue(s.vaultFolder)
        .onChange(async (v) => {
          problem.setText(rootProblem(v, this.folderExists));
          await save("vaultFolder", v);
        });
      problem.setText(rootProblem(s.vaultFolder, this.folderExists));
    });

    new Setting(containerEl)
      .setName("Connection")
      .setDesc(this.plugin.statusText())
      .addButton((b) =>
        b.setButtonText("Reconnect").onClick(async () => {
          await this.plugin.connect(true);
          this.display();
        }),
      );

    drawTeamAndConnections(containerEl, this.team, this.connections);
  }
}

function drawTeamAndConnections(el: HTMLElement, team: TeamSettingsSection, connections: ConnectionsSection): void {
  const conn = el.createDiv({ cls: "okvault-conn-settings" });
  // Team settings first: the connections helper prefills from its draft.
  void team.display(el.createDiv({ cls: "okvault-team-settings" })).then(() => connections.display(conn));
}
