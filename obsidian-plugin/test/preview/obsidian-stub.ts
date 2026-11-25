// Just enough of the Obsidian API to render the plugin's views in a browser (preview only).
type Attrs = { cls?: string; text?: string; type?: string; placeholder?: string; attr?: Record<string, string> };
const P = HTMLElement.prototype as any;
P.createEl = function (tag: string, o: Attrs = {}) {
  const e = document.createElement(tag);
  if (o.cls) e.className = o.cls;
  if (o.text) e.textContent = o.text;
  if (o.type) (e as HTMLInputElement).type = o.type;
  if (o.placeholder) (e as HTMLInputElement).placeholder = o.placeholder;
  for (const [k, v] of Object.entries(o.attr ?? {})) e.setAttribute(k, v);
  this.appendChild(e);
  return e;
};
P.createDiv = function (o?: Attrs) { return this.createEl("div", o); };
P.createSpan = function (o?: Attrs) { return this.createEl("span", o); };
P.empty = function () { this.innerHTML = ""; };
P.setText = function (t: string) { this.textContent = t; };
P.addClass = function (c: string) { this.classList.add(c); };
P.toggleClass = function (c: string, on: boolean) { this.classList.toggle(c, on); };
P.setAttr = function (k: string, v: string) { this.setAttribute(k, v); };
P.hide = function () { this.style.display = "none"; };
P.appendText = function (t: string) { this.append(document.createTextNode(t)); };
P.show = function () { this.style.display = ""; };
(globalThis as any).createDiv = (o?: Attrs) => document.createElement("div").createDiv(o);
const svg = (tag: string, o: { attr?: Record<string, string | number> } = {}, parent?: Element) => {
  const e = document.createElementNS("http://www.w3.org/2000/svg", tag);
  for (const [k, v] of Object.entries(o.attr ?? {})) e.setAttribute(k, String(v));
  parent?.appendChild(e);
  return e;
};
(globalThis as any).createSvg = (tag: string, o?: { attr?: Record<string, string | number> }) => svg(tag, o);
P.createSvg = function (tag: string, o?: { attr?: Record<string, string | number> }) { return svg(tag, o, this); };

export class Component {
  registerEvent(_: unknown) {}
  registerDomEvent(el: HTMLElement, ev: string, fn: (e: Event) => void) { el.addEventListener(ev, fn); }
}
export class ItemView extends Component {
  contentEl: HTMLElement;
  app: any;
  constructor(leaf: any) { super(); this.app = leaf.app; this.contentEl = leaf.el; }
}
export class WorkspaceLeaf {}
export const MarkdownRenderer = {
  async render(_app: unknown, md: string, el: HTMLElement) {
    el.innerHTML = md
      .replace(/&/g, "&amp;").replace(/</g, "&lt;")
      .replace(/`([^`]+)`/g, "<code>$1</code>")
      .replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a class="internal-link" data-href="$2">$1</a>')
      .split(/\n\n+/).map((p) => (p.startsWith("- ") ? "<ul>" + p.split("\n").map((l) => "<li>" + l.slice(2) + "</li>").join("") + "</ul>" : "<p>" + p + "</p>")).join("");
  },
};
export function setIcon(el: HTMLElement, _name: string) { el.textContent = "✓"; }
export function debounce<T extends (...a: any[]) => void>(fn: T, ms: number) {
  let t: number | undefined;
  return ((...a: any[]) => { clearTimeout(t); t = window.setTimeout(() => fn(...a), ms); }) as T;
}
(globalThis as any).createEl = (tag: string, o?: Attrs) => (document.createElement("div") as any).createEl(tag, o);

export class TFolder {
  constructor(public path: string) {}
  isRoot() { return this.path === "/"; }
}
export class Notice {
  constructor(msg: string) { console.log("Notice:", msg); const n = document.body.createDiv({ cls: "preview-notice", text: msg }); setTimeout(() => n.remove(), 4000); }
  hide() {}
}
export class PluginSettingTab {}
type Cb<T> = (c: T) => unknown;
export class Setting {
  settingEl: HTMLElement; infoEl: HTMLElement; nameEl: HTMLElement; descEl: HTMLElement; controlEl: HTMLElement;
  constructor(el: HTMLElement) {
    this.settingEl = el.createDiv({ cls: "setting-item" });
    this.infoEl = this.settingEl.createDiv({ cls: "setting-item-info" });
    this.nameEl = this.infoEl.createDiv({ cls: "setting-item-name" });
    this.descEl = this.infoEl.createDiv({ cls: "setting-item-description" });
    this.controlEl = this.settingEl.createDiv({ cls: "setting-item-control" });
  }
  setName(n: string) { this.nameEl.setText(n); return this; }
  setDesc(d: string) { this.descEl.prepend(document.createTextNode(d)); return this; }
  setHeading() { this.settingEl.addClass("setting-item-heading"); return this; }
  setClass(c: string) { this.settingEl.addClass(c); return this; }
  addText(cb: Cb<any>) {
    const inputEl = this.controlEl.createEl("input", { type: "text" }) as HTMLInputElement;
    const t: any = { inputEl, setPlaceholder: (p: string) => ((inputEl.placeholder = p), t), setValue: (v: string) => ((inputEl.value = v), t), onChange: (f: (v: string) => void) => (inputEl.addEventListener("input", () => f(inputEl.value)), t) };
    cb(t); return this;
  }
  addDropdown(cb: Cb<any>) {
    const sel = this.controlEl.createEl("select") as HTMLSelectElement;
    const d: any = { addOption: (v: string, l: string) => (sel.createEl("option", { text: l, attr: { value: v } }), d), setValue: (v: string) => ((sel.value = v), d), onChange: (f: (v: string) => void) => (sel.addEventListener("change", () => f(sel.value)), d) };
    cb(d); return this;
  }
  addButton(cb: Cb<any>) {
    const b = this.controlEl.createEl("button") as HTMLButtonElement;
    const o: any = { buttonEl: b, setButtonText: (t: string) => ((b.textContent = t), o), setCta: () => (b.addClass("mod-cta"), o), onClick: (f: () => void) => (b.addEventListener("click", f), o) };
    cb(o); return this;
  }
  addExtraButton(cb: Cb<any>) {
    const b = this.controlEl.createEl("button", { cls: "clickable-icon" }) as HTMLButtonElement;
    const o: any = { setIcon: (i: string) => ((b.textContent = i === "trash" ? "🗑" : i), o), setTooltip: (t: string) => (b.setAttr("title", t), o), onClick: (f: () => void) => (b.addEventListener("click", f), o) };
    cb(o); return this;
  }
}

export class TFile {
  basename: string;
  extension = "md";
  parent: { path: string; isRoot(): boolean } | null;
  constructor(public path: string) {
    this.basename = path.split("/").pop()!.replace(/\.md$/, "");
    const dir = path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : "";
    this.parent = { path: dir || "/", isRoot: () => !dir };
  }
}
export function normalizePath(p: string) { return p.replace(/\\/g, "/").replace(/\/+/g, "/"); }
