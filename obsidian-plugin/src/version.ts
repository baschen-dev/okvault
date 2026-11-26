// Compares the okvault program's version with the plugin's. Both are released
// together under one tag (vX.Y.Z), so an okvault older than the plugin may lack
// what the plugin asks of it. Pure: no Obsidian import, so it is tested under Node.

interface Parsed {
  core: [number, number, number];
  /** -1: a pre-release or Go pseudo-version of core; 0: core itself; 1: commits after core (git describe). */
  rank: number;
}

/**
 * Reads the versions okvault reports: a release (v0.5.2), `git describe` from
 * `just build` (v0.5.2-3-gabc1234[-dirty]), a Go pseudo-version from `go install`
 * at a commit (v0.5.3-0.20261126101500-abc123def456), or a pre-release. A local
 * build without a tag (dev, dev-<commit>) has no version: null.
 */
export function parseVersion(v: string): Parsed | null {
  const m = /^v?(\d+)\.(\d+)\.(\d+)(.*)$/.exec(v.trim());
  if (!m) return null;
  const core: [number, number, number] = [Number(m[1]), Number(m[2]), Number(m[3])];
  const rest = m[4].replace(/\+.*$/, ""); // build metadata does not order
  if (rest === "") return { core, rank: 0 };
  if (/^-\d+-g[0-9a-f]+(-dirty)?$/.test(rest)) return { core, rank: 1 };
  return { core, rank: -1 };
}

/** <0 when a is older than b, 0 when equal, >0 when newer; null when either has no version. */
export function compareVersions(a: string, b: string): number | null {
  const pa = parseVersion(a);
  const pb = parseVersion(b);
  if (!pa || !pb) return null;
  for (let i = 0; i < 3; i++) if (pa.core[i] !== pb.core[i]) return pa.core[i] - pb.core[i];
  return pa.rank - pb.rank;
}

/** Why the okvault program is too old for this plugin, or "" when it is not (or cannot tell). */
export function versionProblem(server: string, plugin: string): string {
  const c = compareVersions(server, plugin);
  if (c === null || c >= 0) return "";
  return `okvault ${server} is older than this plugin (${plugin}); some actions may fail. Update the okvault program to ${plugin} or later.`;
}
