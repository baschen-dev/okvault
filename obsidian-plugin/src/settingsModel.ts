// The plugin's own settings: defaults, and how a typed value is cleaned up and
// checked before it is saved. Pure: no Obsidian import, so it is tested under Node.
import { normalizeRoot } from "./paths.ts";

export interface OkvaultSettings {
  /** okvault's MCP endpoint, as printed by `okvault serve --port N`. */
  serverUrl: string;
  /** Start `okvault serve` when nothing answers at serverUrl. */
  autoStart: boolean;
  /** okvault binary: a name on PATH or an absolute path. */
  binaryPath: string;
  /** "follow": the server commits and syncs as .okvault.yaml says; "off": read-only, git off. */
  versioning: "follow" | "off";
  /** okvault root: a folder of this Obsidian vault, relative to it ("" = the vault itself). */
  vaultFolder: string;
  /** Bundle map filters: types and sections left out of the graph. */
  graphHiddenTypes: string[];
  graphHiddenSections: string[];
  /** Bundle map filter: when not empty, only notes with one of these review reasons. */
  graphOnlyReasons: string[];
}

export const DEFAULT_SETTINGS: OkvaultSettings = {
  // Not 8765, okvault's own --http default: a plugin-started read-only server must never
  // take the port Claude Code is told to use.
  serverUrl: "http://127.0.0.1:8766/mcp",
  autoStart: true,
  binaryPath: "okvault",
  versioning: "follow",
  vaultFolder: "",
  graphHiddenTypes: [],
  graphHiddenSections: [],
  graphOnlyReasons: [],
};

/** The settings form edits these; the bundle map's filters are kept elsewhere. */
export type EditableKey = "serverUrl" | "autoStart" | "binaryPath" | "versioning" | "vaultFolder";

/** Why the okvault root cannot be used, or "" when it can. */
export function rootProblem(raw: string, folderExists: (path: string) => boolean): string {
  const n = normalizeRoot(raw);
  if (n === null) return "Must stay inside this vault (no `..`).";
  if (n && !folderExists(n)) return `No folder ${n}/ in this vault.`;
  return "";
}

/**
 * Cleans up a value from the settings form before it is stored: trims text,
 * falls back to the default for an empty URL or binary, and normalizes the
 * okvault root. Returns undefined when the value must not be stored.
 */
export function cleanSetting(key: string, value: unknown): OkvaultSettings[EditableKey] | undefined {
  switch (key) {
    case "serverUrl":
      return String(value ?? "").trim() || DEFAULT_SETTINGS.serverUrl;
    case "binaryPath":
      return String(value ?? "").trim() || DEFAULT_SETTINGS.binaryPath;
    case "autoStart":
      return value === true;
    case "versioning":
      return value === "off" ? "off" : "follow";
    case "vaultFolder":
      return normalizeRoot(String(value ?? "")) ?? undefined;
    default:
      return undefined;
  }
}
