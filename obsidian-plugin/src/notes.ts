import { TFile, normalizePath } from "obsidian";
import type OkvaultPlugin from "./main";
import { fillTemplate, notePath } from "./noteFacts";

export interface Template {
  path: string; // "" = no template file, plain frontmatter
  label: string;
  type: string;
}

/** The vault's _templates notes, with the type each declares. */
export function templates(plugin: OkvaultPlugin): Template[] {
  const app = plugin.app;
  const dir = plugin.toObsidian("_templates");
  return app.vault
    .getMarkdownFiles()
    .filter((f) => f.parent?.path === dir)
    .map((f) => ({ path: f.path, label: f.basename, type: String(app.metadataCache.getFileCache(f)?.frontmatter?.type ?? f.basename) }));
}

/** The template for a type, or a plain one. */
export function templateFor(plugin: OkvaultPlugin, type: string): Template {
  return templates(plugin).find((t) => t.type.toLowerCase() === type.toLowerCase()) ?? { path: "", label: type, type };
}

/**
 * Creates okvault-path folder/kebab(title).md from a template, then lets
 * `edit` adjust its frontmatter (Obsidian's processFrontMatter) and appends
 * `body`. Returns the file, or a reason when it was not created.
 */
export async function createNote(
  plugin: OkvaultPlugin,
  o: { template: Template; title: string; folder: string; edit?: (fm: Record<string, unknown>) => void; body?: string },
): Promise<{ file?: TFile; rel: string; error?: string }> {
  const app = plugin.app;
  const rel = notePath(o.folder, o.title);
  if (!rel) return { rel: "", error: "the title has no letters or digits" };
  const path = normalizePath(plugin.toObsidian(rel));
  if (app.vault.getAbstractFileByPath(path)) return { rel, error: `${rel} already exists` };
  let text = `---\ntype: ${o.template.type}\ntitle: ""\n---\n\n`;
  const tf = o.template.path ? app.vault.getAbstractFileByPath(o.template.path) : null;
  if (tf instanceof TFile) text = await app.vault.read(tf);
  text = fillTemplate(text, o.template.type, o.title);
  if (o.body) text = `${text.replace(/\s*$/, "")}\n\n${o.body}\n`;
  const dir = path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : "";
  if (dir && !app.vault.getAbstractFileByPath(dir)) await app.vault.createFolder(dir);
  const file = await app.vault.create(path, text);
  await app.fileManager.processFrontMatter(file, (fm: Record<string, unknown>) => {
    if (!fm.title) fm.title = o.title;
    o.edit?.(fm);
  });
  return { file, rel };
}
