// Values from frontmatter and YAML are whatever someone typed: a list or a map
// where text was expected must not show up as "[object Object]". Pure: tested
// under Node.

/** A scalar (text, number, true/false) as text; anything else as "". */
export function scalar(v: unknown): string {
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean" || typeof v === "bigint") return String(v);
  return "";
}
