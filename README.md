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
okvault serve  [--vault DIR] [--read-only] [--author NAME] [--no-watch]
okvault ask    [--vault DIR] [--json] QUESTION...
okvault search [--vault DIR] [--type T] [--tag T] [--limit N] [--json] QUERY...
okvault lint   [--vault DIR] [--strict]      # OKF conformance (errors) + hygiene (warnings)
okvault index  [--vault DIR]                 # regenerate okvault-owned index.md files, repair log.md after merges
okvault ticket [--vault DIR] [--json] KEY    # ticket + related notes
okvault review [--vault DIR] [--owner O] [--reason R] [--json]
okvault verify [--vault DIR] [--by NAME] PATH...   # default name: git config user.name
```

## Development

```sh
just test         # vet + unit + in-memory MCP + real stdio smoke test
just lint index   # check the vault, refresh indexes
just win::test    # Windows (PowerShell 7)
```

CI fails if the vault fails lint or if a generated `index.md` is out of date.
