import { App, PluginSettingTab, Setting } from "obsidian";
import type OkvaultPlugin from "./main";
import { TeamSettingsSection } from "./teamSettings";

export interface OkvaultSettings {
  /** okvault's MCP endpoint, as printed by `okvault serve --port N`. */
  serverUrl: string;
  /** Start `okvault serve` when nothing answers at serverUrl. */
  autoStart: boolean;
  /** okvault binary: a name on PATH or an absolute path. */
  binaryPath: string;
  /** "follow": the server commits and syncs as .okvault.yaml says; "off": read-only, git off. */
  versioning: "follow" | "off";
  /** Folder of this Obsidian vault that is the okvault vault ("" = the whole vault). */
  vaultFolder: string;
}

export const DEFAULT_SETTINGS: OkvaultSettings = {
  // Not 8765, okvault's own --http default: a plugin-started read-only server must never
  // take the port Claude Code is told to use.
  serverUrl: "http://127.0.0.1:8766/mcp",
  autoStart: true,
  binaryPath: "okvault",
  versioning: "follow",
  vaultFolder: "",
};

export class OkvaultSettingTab extends PluginSettingTab {
  private team: TeamSettingsSection;
  constructor(
    app: App,
    private plugin: OkvaultPlugin,
  ) {
    super(app, plugin);
    this.team = new TeamSettingsSection(plugin);
  }

  override display(): void {
    const { containerEl } = this;
    containerEl.empty();
    const s = this.plugin.settings;
    new Setting(containerEl).setName("This plugin, on this machine").setHeading();

    new Setting(containerEl)
      .setName("Server URL")
      .setDesc("okvault's MCP endpoint. Run `okvault serve --vault <this vault> --port 8766`, or let the plugin start it. The default is 8766, not okvault's 8765, so a server the plugin starts never takes the port you gave Claude Code; point this at that server instead to share it.")
      .addText((t) =>
        t
          .setPlaceholder(DEFAULT_SETTINGS.serverUrl)
          .setValue(s.serverUrl)
          .onChange(async (v) => {
            s.serverUrl = v.trim() || DEFAULT_SETTINGS.serverUrl;
            await this.plugin.saveSettings();
          }),
      );

    new Setting(containerEl)
      .setName("Start okvault automatically")
      .setDesc(
        "When nothing answers at the server URL, run `okvault serve` on its port for this vault (with the versioning below), and stop it when Obsidian closes.",
      )
      .addToggle((t) =>
        t.setValue(s.autoStart).onChange(async (v) => {
          s.autoStart = v;
          await this.plugin.saveSettings();
        }),
      );

    new Setting(containerEl)
      .setName("Versioning")
      .setDesc(
        "For the server this plugin starts. Follow .okvault.yaml: okvault keeps index.md files current as you edit and, when git.mode is set, commits your edits, rebases, pushes and opens the pull request, as the team configured. Off: read-only; nothing is written or committed. A server you run yourself keeps its own flags.",
      )
      .addDropdown((d) =>
        d
          .addOption("follow", "Follow .okvault.yaml")
          .addOption("off", "Off (read-only)")
          .setValue(s.versioning)
          .onChange(async (v) => {
            s.versioning = v as "follow" | "off";
            await this.plugin.saveSettings();
          }),
      );

    new Setting(containerEl)
      .setName("okvault binary")
      .setDesc("`okvault` if it is on your PATH (go install puts it in $GOPATH/bin), else its full path.")
      .addText((t) =>
        t
          .setPlaceholder("okvault")
          .setValue(s.binaryPath)
          .onChange(async (v) => {
            s.binaryPath = v.trim() || "okvault";
            await this.plugin.saveSettings();
          }),
      );

    new Setting(containerEl)
      .setName("okvault folder")
      .setDesc(
        "Leave empty when this Obsidian vault is the okvault vault (the usual setup: open `vault/` as the Obsidian vault). If you opened the repository root instead, enter `vault`.",
      )
      .addText((t) =>
        t
          .setPlaceholder("(whole vault)")
          .setValue(s.vaultFolder)
          .onChange(async (v) => {
            s.vaultFolder = v.trim().replace(/^\/+|\/+$/g, "");
            await this.plugin.saveSettings();
          }),
      );

    new Setting(containerEl)
      .setName("Connection")
      .setDesc(this.plugin.statusText())
      .addButton((b) =>
        b.setButtonText("Reconnect").onClick(async () => {
          await this.plugin.connect(true);
          this.display();
        }),
      );

    void this.team.display(containerEl.createDiv({ cls: "okvault-team-settings" }));
  }
}
