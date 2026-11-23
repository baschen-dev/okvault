// Starts and stops a local `okvault serve` for the open vault, and runs okvault
// commands (sync, verify) in it. Desktop only. No Obsidian import, so tests run it under Node.
import { spawn, execFile, type ChildProcess } from "child_process";
import { mkdtempSync, rmSync, writeFileSync } from "fs";
import { tmpdir } from "os";
import { join } from "path";
import type { GitStatus } from "./client.ts";

export interface SpawnOptions {
  binary: string;
  vaultDir: string;
  host: string;
  port: number;
  /** true: commit, push and open pull requests as .okvault.yaml says; false: read-only, git off. */
  versioning: boolean;
}

export class OkvaultProcess {
  private proc: ChildProcess | null = null;
  private stderr: string[] = [];
  exitInfo = "";

  get running(): boolean {
    return this.proc !== null && this.proc.exitCode === null;
  }

  start(o: SpawnOptions): void {
    this.stop();
    this.stderr = [];
    this.exitInfo = "";
    const args = ["serve", "--vault", o.vaultDir, "--host", o.host, "--port", String(o.port), "--author", "obsidian"];
    // Without --read-only okvault also keeps index.md files in step with edits and,
    // when .okvault.yaml turns git on, commits and syncs. --read-only turns all of that off.
    if (!o.versioning) args.push("--read-only", "--git", "off");
    const p = spawn(o.binary, args, { windowsHide: true, stdio: ["ignore", "ignore", "pipe"] });
    this.proc = p;
    p.stderr?.on("data", (b: Buffer) => {
      this.stderr.push(...b.toString().split(/\r?\n/).filter(Boolean));
      if (this.stderr.length > 50) this.stderr.splice(0, this.stderr.length - 50);
    });
    p.on("error", (e) => {
      this.exitInfo = `could not run ${o.binary}: ${e.message}`;
      if (this.proc === p) this.proc = null;
    });
    p.on("exit", (code) => {
      if (!this.exitInfo) this.exitInfo = `okvault exited (${code}): ${this.lastLog()}`;
      if (this.proc === p) this.proc = null;
    });
  }

  lastLog(n = 3): string {
    return this.stderr.slice(-n).join(" | ");
  }

  stop(): void {
    if (this.proc && this.proc.exitCode === null) this.proc.kill();
    this.proc = null;
  }
}

export interface CliResult {
  code: number;
  stdout: string;
  stderr: string;
}

/**
 * Runs `okvault ARGS` and returns its output. The timeout guards against git
 * waiting on a credential prompt (okvault runs SSH in batch mode, but a
 * credential helper can still block).
 */
export function runOkvault(binary: string, args: string[], cwd: string, timeoutMs = 120_000): Promise<CliResult> {
  return new Promise((resolve) => {
    execFile(binary, args, { cwd, timeout: timeoutMs, windowsHide: true, maxBuffer: 4 << 20 }, (err, stdout, stderr) => {
      const e = err as (NodeJS.ErrnoException & { code?: number | string; killed?: boolean }) | null;
      let code = 0;
      if (e) code = typeof e.code === "number" ? e.code : 1;
      if (e?.killed) stderr = `${stderr}\nokvault ${args[0]} timed out after ${Math.round(timeoutMs / 1000)}s`;
      else if (e && typeof e.code === "string") stderr = `${stderr}\ncould not run ${binary}: ${e.message}`;
      resolve({ code, stdout: String(stdout), stderr: String(stderr).trim() });
    });
  });
}

/** `okvault sync --json`: commit edits, rebase, push, update the pull request. */
export async function syncNow(binary: string, vaultDir: string): Promise<{ status?: GitStatus; error?: string }> {
  const r = await runOkvault(binary, ["sync", "--vault", vaultDir, "--json"], vaultDir);
  const lastLine = (s: string) => s.split(/\r?\n/).filter((l) => l.trim() && !l.startsWith("time=")).pop() ?? "";
  if (r.code !== 0) return { error: lastLine(r.stderr) || lastLine(r.stdout) || `okvault sync exited ${r.code}` };
  try {
    return { status: JSON.parse(r.stdout) as GitStatus };
  } catch {
    return { error: `unexpected output from okvault sync: ${r.stdout.slice(0, 200)}` };
  }
}

/** `okvault verify`: records that a person checked the note (and commits it when git is on). */
export async function verifyNote(binary: string, vaultDir: string, path: string, by?: string): Promise<{ ok: boolean; message: string }> {
  const args = ["verify", "--vault", vaultDir];
  if (by) args.push("--by", by);
  args.push(path);
  const r = await runOkvault(binary, args, vaultDir);
  const out = [r.stdout, r.stderr]
    .join("\n")
    .split(/\r?\n/)
    .filter((l) => l.trim() && !l.startsWith("time="))
    .join("\n");
  return { ok: r.code === 0, message: out || (r.code === 0 ? `verified ${path}` : `okvault verify exited ${r.code}`) };
}

/**
 * Has okvault itself check a draft .okvault.yaml: lint an empty vault holding
 * only that file, so the errors are okvault's own (unknown keys, bad intervals...).
 * Returns the problems; empty means okvault accepts it.
 */
export async function checkConfig(binary: string, yamlText: string): Promise<{ errors: string[]; unavailable?: string }> {
  const dir = mkdtempSync(join(tmpdir(), "okvault-config-"));
  try {
    writeFileSync(join(dir, ".okvault.yaml"), yamlText);
    const r = await runOkvault(binary, ["lint", "--vault", dir], dir, 20_000);
    // "error   .okvault.yaml: msg", possibly followed by indented detail lines.
    const errors: string[] = [];
    for (const l of r.stdout.split(/\r?\n/)) {
      if (/^error\s/.test(l)) errors.push(l.replace(/^error\s+/, "").replace(/^\.okvault\.yaml:\s*/, ""));
      else if (/^\s+\S/.test(l) && errors.length) errors[errors.length - 1] += " " + l.trim();
    }
    for (let i = 0; i < errors.length; i++) {
      // yaml.v3's "field token not found in type config.Config" means an unknown key.
      errors[i] = errors[i].replace(/yaml: unmarshal errors: line (\d+): field (\S+) not found in type \S+/, "line $1: unknown key \"$2\"");
    }
    if (r.code !== 0 && !errors.length) return { errors: [], unavailable: r.stderr.split(/\r?\n/).pop() || `okvault lint exited ${r.code}` };
    return { errors };
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

export interface Finding {
  severity: "error" | "warning";
  path: string;
  message: string;
}

/** `okvault lint` on the whole vault, grouped by note path. */
export async function lintVault(binary: string, vaultDir: string): Promise<{ findings: Map<string, Finding[]>; error?: string }> {
  const r = await runOkvault(binary, ["lint", "--vault", vaultDir], vaultDir, 60_000);
  const findings = new Map<string, Finding[]>();
  let last: Finding | null = null;
  for (const l of r.stdout.split(/\r?\n/)) {
    const m = /^(error|warning)\s+(.+?): (.*)$/.exec(l);
    if (m) {
      last = { severity: m[1] as Finding["severity"], path: m[2], message: m[3] };
      if (!findings.has(last.path)) findings.set(last.path, []);
      findings.get(last.path)!.push(last);
    } else if (last && /^\s+\S/.test(l)) {
      last.message += " " + l.trim();
    } else {
      last = null;
    }
  }
  // Exit 1 just means errors were found; no summary line means okvault did not run.
  if (!/\d+ notes, \d+ errors, \d+ warnings/.test(r.stdout)) return { findings, error: r.stderr.split(/\r?\n/).pop() || "okvault lint did not run" };
  return { findings };
}
