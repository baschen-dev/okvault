// Demo state for the versioning panel in the browser preview (?demo=1).
export const demoTeam = {
  teamGit: { mode: "pr", base: "main", commitEdits: "10m", pullInterval: "5m" },
  git: {
    mode: "pr",
    branch: "okvault/alice",
    base: "main",
    unpushed: 2,
    pull_request: "https://github.com/acme/kb/pull/12",
    problem: "okvault/alice: rebase stopped on reference/search-ranking.md (a teammate changed the same note); your commits are unchanged. Merge by hand, then run okvault sync.",
    last_sync: new Date(Date.now() - 4 * 60_000).toISOString(),
  },
  obsidianGit: { enabled: true, auto: ["Auto commit-and-sync interval", "Pull on startup"], pushEnabled: true },
  reviewItems: [
    { path: "faq/how-do-i-add-an-faq.md", title: "How do I add an FAQ entry?", type: "FAQ", reasons: ["unverified"], generated: { by: "claude-code", at: "2026-11-20T10:00:00Z" }, owners: ["team-docs"] },
    { path: "reference/search-ranking.md", title: "Search ranking", type: "Reference", reasons: ["stale"], last_verified: { by: "bob", at: "2026-04-02T10:00:00Z" }, detail: "stale since 2026-10-01" },
  ],
};
