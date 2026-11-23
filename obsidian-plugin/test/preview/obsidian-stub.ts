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
P.show = function () { this.style.display = ""; };
(globalThis as any).createDiv = (o?: Attrs) => document.createElement("div").createDiv(o);

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
