// Event handlers and fire-and-forget calls that run async work. A failure
// would otherwise end as an unhandled rejection in the console; here it is
// logged and shown, so a click that did nothing says why.
import { Notice } from "obsidian";

function report(e: unknown): void {
  console.error("okvault:", e);
  new Notice(`okvault: ${e instanceof Error ? e.message : String(e)}`, 10_000);
}

/** An event handler that runs fn and shows its failure. fn starts synchronously, so it can still call preventDefault. */
export function act<A extends unknown[]>(fn: (...args: A) => unknown): (...args: A) => void {
  return (...args: A) => {
    try {
      const r = fn(...args);
      if (r instanceof Promise) r.catch(report);
    } catch (e) {
      report(e);
    }
  };
}

/** Lets a promise run on without waiting for it, showing its failure. */
export function background(p: Promise<unknown>): void {
  p.catch(report);
}
