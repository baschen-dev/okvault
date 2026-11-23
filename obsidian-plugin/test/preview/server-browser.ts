// Browser stand-in for src/server.ts (which needs Node): okvault checks go through the preview server.
export async function checkConfig(_binary: string, yamlText: string) {
  return (await fetch("/check-config", { method: "POST", body: yamlText })).json();
}
export const syncNow = async () => ({ error: "not in the preview" });
export const verifyNote = async () => ({ ok: false, message: "not in the preview" });
export class OkvaultProcess {}
export async function lintVault(_b: string, _d: string) {
  const r = await (await fetch("/lint")).json();
  return { findings: new Map(Object.entries(r.findings)), error: r.error };
}
