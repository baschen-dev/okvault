# okvault

okvault is a knowledge vault for AI agents. You write notes in **Obsidian**. They are stored in the **[Open Knowledge Format (OKF v0.2)](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md)**. Agents like Claude Code read them through **MCP**, with ranked search and instant FAQ answers.

- **Readable.** Every note is plain Markdown with YAML frontmatter. You edit it in Obsidian, review it on GitHub, and diff it in git.
- **Fast.** okvault builds an in-memory BM25 index ([bleve](https://github.com/blevesearch/bleve)) at start-up. The index is rebuilt within about 300 ms of any file change. Lookups take under a millisecond.
- **FAQ fast path.** `kb_ask` returns a stored answer only when the question matches an FAQ confidently. Otherwise it points the agent at the most relevant notes.
- **Fully committable.** No database and no index files live in the vault. Obsidian settings are committed and per-device state is ignored.
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

## MCP tools

| Tool | What it does |
|---|---|
| `kb_ask` | Answers a natural-language question from FAQ notes. Returns `confident`, `answer` and `matched_question`, or related hits if no FAQ matches well enough. |
| `kb_search` | BM25 search over title, aliases, description, tags and body, with `type`, `tags` and `dir` filters. Returns compact hits. |
| `kb_get` | Reads one note: frontmatter, body, links, backlinks and broken links. Accepts a path or a title. |
| `kb_list` | Counts notes per type, tag and folder, and lists the notes that match the filters. |
| `kb_write` | Creates or updates a note. It requires `type`, keeps unknown keys, stamps `generated`, refreshes `index.md` and appends to `log.md`. Disabled with `--read-only`. |

## Vault layout

```
vault/
  .obsidian/            committed settings: markdown links, relative paths, templates
  _templates/           Obsidian templates (not indexed)
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
okvault index  [--vault DIR]                 # regenerate okvault-owned index.md files
```

## Development

```sh
just test         # vet + unit + in-memory MCP + real stdio smoke test
just lint index   # check the vault, refresh indexes
just win::test    # Windows (PowerShell 7)
```

CI fails if the vault fails lint or if a generated `index.md` is out of date.
