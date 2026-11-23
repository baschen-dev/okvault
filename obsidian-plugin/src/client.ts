// A small MCP client for okvault's Streamable HTTP endpoint (okvault serve --port N).
// It has no Obsidian import so it runs under Node too (see test/run.ts).

/** One HTTP POST. Obsidian passes requestUrl (no CORS), tests pass fetch. */
export type Post = (
  url: string,
  headers: Record<string, string>,
  body: string,
) => Promise<{ status: number; headers: Record<string, string>; text: string }>;

export interface Hit {
  path: string;
  title: string;
  type: string;
  description?: string;
  tags?: string[];
  tickets?: string[];
  snippet?: string;
  score: number;
}

export interface Answer {
  confident: boolean;
  confidence: number;
  path?: string;
  matched_question?: string;
  answer?: string;
  related?: Hit[];
}

export interface SearchArgs {
  query: string;
  type?: string;
  tags?: string[];
  dir?: string;
  ticket?: string;
  owner?: string;
  limit?: number;
  with_deprecated?: boolean;
}

export interface Stamp {
  by: string;
  at: string;
}

export interface ReviewItem {
  path: string;
  title: string;
  type: string;
  owners?: string[];
  reasons: string[];
  generated?: Stamp;
  last_verified?: Stamp;
  detail?: string;
}

export interface TicketLink {
  key: string;
  url?: string;
}

/** kb_get */
export interface NoteDetail {
  path: string;
  title: string;
  type: string;
  description?: string;
  revision: string;
  owners?: string[];
  tickets?: TicketLink[];
  frontmatter: Record<string, unknown>;
  body: string;
  links?: string[];
  backlinks?: string[];
  broken_links?: string[];
}

export interface Entry {
  path: string;
  title: string;
  type: string;
  description?: string;
}

/** tracker.Issue */
export interface Issue {
  key: string;
  url?: string;
  summary: string;
  type?: string;
  status?: string;
  status_category?: string;
  priority?: string;
  assignee?: string;
  reporter?: string;
  labels?: string[];
  parent?: string;
  updated?: string;
  description?: string;
  recent_comments?: { author: string; created: string; body: string }[];
}

/** kb_ticket */
export interface TicketContext {
  key: string;
  url?: string;
  ticket?: Issue;
  tracker_note?: string;
  linked?: Entry[];
  mentioned?: Entry[];
  suggested?: Hit[];
  hint: string;
}

/** okvault's git sync state (gitsync.Status), from kb_review's git field or `okvault sync --json`. */
export interface GitStatus {
  mode: string;
  branch?: string;
  base?: string;
  unpushed?: number;
  pull_request?: string;
  problem?: string;
  last_sync?: string;
}

export interface Review {
  total: number;
  items: ReviewItem[];
  /** present only when the server syncs with git (not read-only, mode not off) */
  git?: GitStatus;
}

const PROTOCOL = "2025-06-18";

export class McpError extends Error {
  readonly status?: number;
  constructor(message: string, status?: number) {
    super(message);
    this.status = status;
  }
}

export class OkvaultClient {
  private session: string | null = null;
  private initializing: Promise<void> | null = null;
  private nextId = 1;
  serverVersion = "";

  private url: string;
  private post: Post;

  constructor(url: string, post: Post) {
    this.url = url;
    this.post = post;
  }

  setUrl(url: string): void {
    if (url !== this.url) {
      this.url = url;
      this.session = null;
    }
  }

  async ask(question: string): Promise<Answer> {
    return this.call<Answer>("kb_ask", { question });
  }

  async search(args: SearchArgs): Promise<Hit[]> {
    const out = await this.call<{ hits: Hit[] | null }>("kb_search", args);
    return out.hits ?? [];
  }

  /** kb_get: one note with its okvault view (owners incl. folder defaults, ticket links, backlinks, broken links). */
  async get(path: string): Promise<NoteDetail> {
    return this.call<NoteDetail>("kb_get", { path });
  }

  /** kb_ticket: the ticket from the tracker (when configured) and the notes about it. */
  async ticket(key: string): Promise<TicketContext> {
    return this.call<TicketContext>("kb_ticket", { key });
  }

  /** Note paths the server knows (first 200), to check it serves this vault. */
  async paths(): Promise<string[]> {
    const out = await this.call<{ notes: { path: string }[] | null }>("kb_list", {});
    return (out.notes ?? []).map((n) => n.path);
  }

  async review(limit = 200): Promise<Review> {
    return this.call<Review>("kb_review", { limit });
  }

  /** True when the server answers initialize. */
  async ping(): Promise<boolean> {
    try {
      this.session = null;
      await this.ensureSession();
      return true;
    } catch {
      return false;
    }
  }

  /** Calls a tool and returns its structured result. Re-initializes once if the session is gone (server restarted). */
  async call<T>(name: string, args: object): Promise<T> {
    await this.ensureSession();
    try {
      return await this.callOnce<T>(name, args);
    } catch (e) {
      if (e instanceof McpError && (e.status === 404 || e.status === 400)) {
        this.session = null;
        await this.ensureSession();
        return this.callOnce<T>(name, args);
      }
      throw e;
    }
  }

  private async callOnce<T>(name: string, args: object): Promise<T> {
    const res = (await this.rpc("tools/call", { name, arguments: args })) as {
      content?: { type: string; text?: string }[];
      structuredContent?: T;
      isError?: boolean;
    };
    const text = res.content?.find((c) => c.type === "text")?.text ?? "";
    if (res.isError) throw new McpError(text || `${name} failed`);
    if (res.structuredContent !== undefined) return res.structuredContent;
    return JSON.parse(text) as T;
  }

  private ensureSession(): Promise<void> {
    if (this.session) return Promise.resolve();
    if (!this.initializing) {
      this.initializing = this.initialize().finally(() => {
        this.initializing = null;
      });
    }
    return this.initializing;
  }

  private async initialize(): Promise<void> {
    const res = (await this.rpc(
      "initialize",
      {
        protocolVersion: PROTOCOL,
        capabilities: {},
        clientInfo: { name: "okvault-obsidian", version: "0.1.0" },
      },
      true,
    )) as { serverInfo?: { version?: string } };
    this.serverVersion = res.serverInfo?.version ?? "";
    await this.send({ jsonrpc: "2.0", method: "notifications/initialized" });
  }

  private async rpc(method: string, params: object, init = false): Promise<unknown> {
    const id = this.nextId++;
    const r = await this.send({ jsonrpc: "2.0", id, method, params }, init);
    const msg = parseMessages(r.text).find((m) => m.id === id);
    if (!msg) throw new McpError(`no response to ${method}`, r.status);
    if (msg.error) throw new McpError(msg.error.message ?? `${method} failed`, r.status);
    return msg.result;
  }

  private async send(
    msg: object,
    init = false,
  ): Promise<{ status: number; headers: Record<string, string>; text: string }> {
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      Accept: "application/json, text/event-stream",
    };
    if (!init) {
      headers["Mcp-Protocol-Version"] = PROTOCOL;
      if (this.session) headers["Mcp-Session-Id"] = this.session;
    }
    let r;
    try {
      r = await this.post(this.url, headers, JSON.stringify(msg));
    } catch (e) {
      throw new McpError(`okvault not reachable at ${this.url}: ${(e as Error).message}`);
    }
    if (r.status >= 400) {
      throw new McpError(`okvault returned HTTP ${r.status}: ${r.text.trim()}`, r.status);
    }
    if (init) {
      const sid = header(r.headers, "mcp-session-id");
      if (!sid) throw new McpError("okvault did not return an Mcp-Session-Id");
      this.session = sid;
    }
    return r;
  }
}

interface RpcMessage {
  id?: number;
  result?: unknown;
  error?: { message?: string };
}

/** Reads JSON-RPC messages from a plain JSON body or from an SSE stream (data: lines). */
export function parseMessages(text: string): RpcMessage[] {
  const t = text.trim();
  if (!t) return [];
  if (t.startsWith("{") || t.startsWith("[")) {
    const v = JSON.parse(t);
    return Array.isArray(v) ? v : [v];
  }
  const out: RpcMessage[] = [];
  for (const event of t.split(/\r?\n\r?\n/)) {
    const data = event
      .split(/\r?\n/)
      .filter((l) => l.startsWith("data:"))
      .map((l) => l.slice(5).replace(/^ /, ""))
      .join("\n");
    if (data) out.push(JSON.parse(data));
  }
  return out;
}

function header(h: Record<string, string>, name: string): string | undefined {
  for (const k of Object.keys(h)) if (k.toLowerCase() === name) return h[k];
  return undefined;
}
