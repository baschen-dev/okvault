# okvault

okvault is a knowledge vault for AI agents. You write notes in **Obsidian**. They are stored in the **[Open Knowledge Format (OKF v0.2)](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md)**. Agents like Claude Code read them through **MCP**, with ranked search and instant FAQ answers.

- **Readable.** Every note is plain Markdown with YAML frontmatter. You edit it in Obsidian, review it on GitHub, and diff it in git.
- **Fast.** okvault builds an in-memory BM25 index ([bleve](https://github.com/blevesearch/bleve)) at start-up. The index is rebuilt within about 300 ms of any file change. Lookups take under a millisecond.
- **FAQ fast path.** `kb_ask` returns a stored answer only when the question matches an FAQ confidently. Otherwise it points the agent at the most relevant notes.
- **Fully committable.** No database and no index files live in the vault. Obsidian settings are committed and per-device state is ignored. While `serve` runs, generated `index.md` files are kept up to date as you edit in Obsidian; after editing with okvault stopped, run `okvault index` (or `just index`) before committing, since CI checks them.
- **Team-ready.** Notes link to Jira tickets, have owners, and carry who wrote and who checked them. `kb_ticket` gives an agent the ticket and the team's notes about it in one call; `kb_review` is the maintenance queue. See [Team workflow](vault/conventions/team-workflow.md).
- **One static Go binary.** No Obsidian process, plugin or model download is needed.

Background and design choices are in [docs/research.md](docs/research.md).

## Quick start

```sh
go install github.com/baschen-dev/okvault/cmd/okvault@latest
okvault ask --vault ./vault "how do I add an faq"
okvault search --vault ./vault bm25 ranking
```

Register it with Claude Code:

```sh
claude mcp add --scope user okvault -- okvault serve --vault /path/to/vault --author claude-code
```

You can also put it in a project's `.mcp.json`:

```json
{
  "mcpServers": {
    "okvault": { "command": "okvault", "args": ["serve", "--vault", "./vault", "--author", "claude-code"] }
  }
}
```

To use the vault in Obsidian, choose *Open folder as vault* and pick `vault/`.

An existing Obsidian vault works too: notes without frontmatter are loaded as untyped notes (searchable, linked, a lint warning each) until someone gives them a `type`.

## MCP tools

| Tool | What it does |
|---|---|
| `kb_ask` | Answers a natural-language question from FAQ notes. Returns `confident`, `answer` and `matched_question`, or related hits if no FAQ matches well enough. |
| `kb_search` | BM25 search over title, aliases, description, tags and body, with `type`, `tags`, `dir`, `ticket` and `owner` filters. A ticket key in the query matches exactly. Returns compact hits. |
| `kb_get` | Reads one note: frontmatter, body, links, backlinks, broken links, tickets, owners and its `revision`. Accepts a path or a title. |
| `kb_list` | Counts notes per type, tag and folder, and lists the notes that match the filters. |
| `kb_ticket` | Context for a ticket key: the ticket from Jira (when configured), notes linked to it or mentioning it, and notes matching its summary. Works without Jira credentials, from the vault alone. |
| `kb_review` | Notes that need a person: stale, unverified agent edits, drafts, broken links, no owner. Filter by owner or reason. |
| `kb_write` | Creates or updates a note. It requires `type`, keeps unknown keys, stamps `generated`, refreshes `index.md` and appends to `log.md`. `base_revision` (from `kb_get`) makes it fail instead of overwriting a teammate's change. Disabled with `--read-only`. |

## Teams and tickets

Knowledge lives in the vault; tasks live in Jira. Configure the link once in `vault/.okvault.yaml` (committed, no secrets):

```yaml
tracker:
  kind: jira
  url: https://acme.atlassian.net
  projects: [PROJ, OPS]      # keys okvault recognises in notes
owners:
  ops: [team-ops]            # folder defaults; a note's own `owners` wins
review_interval: 180d
```

Each person who wants ticket details sets `JIRA_API_TOKEN`, plus `JIRA_EMAIL` on Jira Cloud. Without them the token is sent as a Data Center personal access token, and with no token at all `kb_ticket` still returns the vault's notes. okvault only reads from Jira.

The loop an agent follows (it is in the server instructions):

1. Starting `PROJ-123`: call `kb_ticket` and read the linked notes.
2. Finishing: `kb_write` the decision, gotcha or FAQ with `tickets: [PROJ-123]`, updating existing notes where possible.
3. A person reviews agent edits (`okvault review`) and marks them checked with `okvault verify vault/ops/x.md`. That records `verified: [{by, at}]` and pushes `stale_after` forward.

Git: `log.md` merges by union; on an `index.md` conflict take either side and run `okvault index`. See [the FAQ](vault/faq/how-do-i-resolve-a-merge-conflict.md). Changes to `.okvault.yaml` are picked up while `serve` runs (Jira credentials are read when it starts).

## Git sync and GitHub pull requests

okvault can record every change in git itself. The policy is team-wide, so it lives in `.okvault.yaml`; one machine can override it with `okvault serve --git off|commit|push|pr`.

```yaml
git:
  mode: pr              # off (default) | commit | push | pr
  base: main            # pr: the branch pull requests target (default: the remote's default branch)
  # branch: okvault/alice   # pr: working branch (default okvault/<git user.name>)
  pull_interval: 5m     # push/pr: fetch, rebase and push this often while serving; "0" = only after writes
  commit_edits: 10m     # also commit edits made in Obsidian once the vault has been quiet this long
```

| Mode | What happens |
|---|---|
| `commit` | Each `kb_write` and `okvault verify` is one commit (note, its `index.md`, `log.md`). Nothing leaves the machine. |
| `push` | Also rebases on the branch's upstream and pushes, after each write and every `pull_interval`. For a personal vault, or a team that commits straight to `main`. |
| `pr` | The checkout works on a personal branch, `okvault/<you>`. okvault keeps it rebased on `base`, force-pushes it (only over commits it pushed itself) and keeps one pull request open for it, whose description lists the changes. Teammates review knowledge like code; merging publishes it. |

Details that matter in a team:

- **Only okvault's files are committed.** Commits use `git commit --only` with the paths okvault wrote, so whatever you staged yourself stays staged and out of them. With `commit_edits`, uncommitted changes under the vault folder are committed as `docs(kb): edits made outside okvault`.
- **Generated files never block a rebase.** Conflicts in `index.md` are resolved by regenerating it, and `log.md` conflicts by merging both sides. Index regeneration waits while git is rebasing or merging (by okvault or by you), and okvault processes sharing one checkout (one per Claude Code session) take turns through a lock file in `.git`.
- **Real conflicts stop, they do not guess.** If a teammate changed the same note, the rebase is aborted and your commits stay as they were; `kb_write`, `kb_review` and `okvault sync` report the problem. Fetching, pushing and GitHub calls run outside the vault's write lock, so writes never wait on the network, and SSH runs in batch mode so a passphrase prompt fails instead of hanging. Uncommitted edits to a file that incoming commits also change are never stashed away: okvault refuses and says so.
- **Squash merges are handled.** When the branch's pull request was merged (squash, rebase or merge commit), okvault drops what it carried and opens a new pull request for later changes. This needs the GitHub API; without a token okvault pushes the branch and reports the link to open the pull request by hand.
- **Credentials** come from `GH_TOKEN` or `GITHUB_TOKEN`, else the GitHub CLI's login (`gh auth token`), and git pushes with your usual credential helper or SSH key. Nothing is stored in the vault. GitHub Enterprise Server remotes work too (`https://HOST/api/v3`).
- **Branch protection is GitHub's job.** pr mode is a convention on each machine; to make reviews mandatory, protect `main` on GitHub (rulesets on private repositories need a paid plan).
- **Use `commit_edits` with push and pr modes.** Indexes are generated from the work tree, so a pushed `index.md` can list a note you have not committed yet, and the vault's CI index check fails until it is. `commit_edits` shortens that window to its quiet time; it does not close it.
- **Auto-commit runs while okvault runs**, that is while a Claude Code session has it open. Edits made in Obsidian with no session open are committed by the next session, or by `okvault sync`.
- Everyone on the team should run the same okvault version: `.okvault.yaml` rejects keys a binary does not know, so an older one would ignore the whole file.

`okvault sync` does one pass by hand: commit pending edits, rebase, push, update the pull request. [examples/github/okvault.yml](examples/github/okvault.yml) is a workflow for the vault repository that lints notes and checks indexes on every pull request.

## Vault layout

```
vault/
  .obsidian/            committed settings: markdown links, relative paths, templates
  .okvault.yaml         team settings: tracker, project keys, owners, review interval
  _templates/           Obsidian templates (FAQ, Concept, Decision; not indexed)
  index.md              generated OKF index (root carries okf_version)
  log.md                OKF change log, newest first
  faq/                  type: FAQ  - title = question, aliases = other phrasings, body = answer
  conventions/ reference/ ...
```

Note rules are in [vault/conventions/note-format.md](vault/conventions/note-format.md):
- `type` is required. `title`, `description` and `tags` are recommended.
- Use kebab-case file names.
- Link with relative Markdown links (`[x](../reference/x.md)`).

okvault only rewrites an `index.md` that carries the `<!-- okvault:generated -->` marker. A hand-written index is left alone.

The vault can also live in its own repository. Point `--vault` (or `OKVAULT_DIR`) at it.

## CLI

```
okvault serve  [--vault DIR] [--read-only] [--author NAME] [--no-watch] [--git off|commit|push|pr]
okvault ask    [--vault DIR] [--json] QUESTION...
okvault search [--vault DIR] [--type T] [--tag T] [--limit N] [--json] QUERY...
okvault lint   [--vault DIR] [--strict]      # OKF conformance (errors) + hygiene (warnings)
okvault index  [--vault DIR]                 # regenerate okvault-owned index.md files, repair log.md after merges
okvault ticket [--vault DIR] [--json] KEY    # ticket + related notes
okvault review [--vault DIR] [--owner O] [--reason R] [--json]
okvault verify [--vault DIR] [--by NAME] PATH...   # default name: git config user.name
okvault sync   [--vault DIR] [--git MODE] [--json]   # commit edits, rebase, push or update the pull request
```

## Development

```sh
just test         # vet + unit + in-memory MCP + real stdio smoke test
just lint index   # check the vault, refresh indexes
just win::test    # Windows (PowerShell 7)
```

CI fails if the vault fails lint or if a generated `index.md` is out of date.
