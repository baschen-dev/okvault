# Research: Obsidian + OKF knowledge for agents over MCP

Researched 2026-11-22. This note records the options looked at and why okvault is built the way it is.

## Goal

- Agent knowledge stored as readable Markdown that people edit in Obsidian.
- An index that lets an agent look things up fast, plus FAQ-style instant answers.
- An MCP server that connects the vault to agents such as Claude Code.
- The whole vault can be committed to GitHub: no binary index, no database, no per-device state.
- Written in Go or Rust, not Python.

## Storage format: Open Knowledge Format (OKF)

[OKF](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md) is Google Cloud's vendor-neutral spec for knowledge as Markdown. The spec itself says **v0.2**, which supersedes v0.1. Secondary sources date the v0.1 announcement to June 2026; the primary announcement page could not be fetched.

- **Bundle and concepts.** A bundle is a directory of `.md` files, and each file is one *concept*.
- **Frontmatter.** Each concept starts with YAML frontmatter. Only `type` is required.
- **Recommended keys.** `title`, `description`, `resource` and `tags`.
- **Optional keys.**
  - Provenance: `sources`.
  - Trust: `generated: {by, at}` and `verified`.
  - Lifecycle: `status` (`draft|stable|deprecated`) and `stale_after`.
- **Unknown keys.** Producers may add keys, and consumers must preserve keys they don't know.
- **Reserved files.**
  - `index.md` is a directory listing. It has no frontmatter, except an optional `okf_version` at the root.
  - `log.md` holds `## YYYY-MM-DD` sections, newest first.
- **Links.** Concepts link with standard Markdown links, either `/`-prefixed bundle paths or relative paths. Wikilinks are not part of the spec.
- **Conformance.** A bundle conforms when every file has parseable frontmatter with a non-empty `type`, and `index.md`/`log.md` are well formed. A consumer must not reject a bundle over broken links or unknown types.

### Fit with Obsidian

- **Frontmatter maps to properties.** OKF frontmatter is Obsidian *properties*. `tags` and `aliases` are native Obsidian keys.
  - okvault reuses `aliases` as the list of alternative phrasings for an FAQ, so the same key powers Obsidian's link suggestions and `kb_ask`.
- **Links: relative Markdown links.**
  - Obsidian writes wikilinks by default, and those don't render on GitHub or in OKF tools.
  - The committed `.obsidian/app.json` therefore sets `useMarkdownLinks: true` and `newLinkFormat: relative`.
  - Relative links resolve the same way in Obsidian, on GitHub and for OKF consumers.
  - They were chosen over the `/`-prefixed form that OKF recommends because they are known to work in all three. Whether Obsidian resolves `/`-prefixed links from the vault root was not verified.
  - okvault still parses wikilinks, for hand-written notes.
- **File names.** Kebab-case, so links need no `%20` escaping.
- **What is committed.**
  - Committed: `.obsidian/` settings (`app.json`, `core-plugins.json`, `types.json`, `templates.json`).
  - Ignored: `workspace*.json` (per-device), `.trash/` and plugin data.
  - `.gitattributes` forces LF line endings. The parser also accepts CRLF.
- **Folders that are not knowledge.** Folders starting with `_` (`_templates`, `_attachments`) and dot-folders are not indexed.

## Prior art

| Project | Lang | Notes |
|---|---|---|
| [superops-team/okf](https://github.com/superops-team/okf) | Go, Apache-2.0 | The closest match. It is an OKF v0.2 tool that *generates* a knowledge base from a git repo (`.okf/knowledge`). It offers hybrid MiniLM + BM25 search, document import, and 12+ MCP tools (`okf_query`, `okf_ask`, `okf_note`...). It is repo-centric and large. |
| [cyanheads/obsidian-mcp-server](https://github.com/cyanheads/obsidian-mcp-server) | TS | General vault CRUD, through Obsidian's Local REST API plugin, so Obsidian has to be running. |
| Vault Cortex, vault-memory, enquire-mcp, maxkuminov/obsidian-mcp | mixed | Agent-memory servers over Obsidian vaults. Some use hybrid search (BM25 + embeddings + reranking). Several need pgvector or Ollama, or keep a separate index store. |
| Official MCP `memory` server | TS | A knowledge graph in JSONL. It is neither human-editable Markdown nor Obsidian. |

**How okvault differs:**
- It is Obsidian-first and human-curated. The vault is the source of truth, not something generated from code.
- OKF is the on-disk format.
- It answers FAQs from the vault's own Q&A notes.
- The index is rebuilt in memory, so nothing derived is written to the repo.
- It is a single static Go binary, with no Obsidian process, plugin, database or model download.

If you want repo-scanning and auto-generated knowledge, superops-team/okf covers it. Because both use OKF, the two can share a bundle.

## Retrieval: BM25 first

- **Evidence.** Recent practitioner and preprint results favour plain BM25 for agent lookups over modest corpora:
  - BM25 matched an 8B embedding model at picking tools, at a fraction of the cost ([TDS](https://towardsdatascience.com/keyword-search-tied-an-8b-embedding-model-at-picking-agent-tools/)).
  - BM25 overtook agentic file search past about 10M tokens ([BM25 Wins at Scale](https://huggingface.co/papers/2607.26497.md)).
  - When the agent reformulates the query, the gap to vectors narrows further.
  - Hybrid BM25 + vectors still adds recall in memory benchmarks ([agentmemory](https://raw.githubusercontent.com/rohitg00/agentmemory/HEAD/benchmark/LONGMEMEVAL.md)).
- **Decision.** Use BM25 with field boosts (title > aliases > description/tags > body) and light fuzzy matching.
  - The index lives in memory and is rebuilt at start-up and on file change. A few thousand notes take milliseconds.
  - Embeddings are a possible later addition behind the same `search` package. They should be optional, so the binary stays dependency-free.
  - **Link priors (added 2026-11-26).** The note-link graph adjusts the BM25 order a little: notes other notes link to, and notes one link from a ticket in the query, rank higher. The adjustments multiply the score and are capped, so they only decide between notes that match about equally well ([Search ranking](../vault/reference/search-ranking.md)).
- **FAQ fast path.**
  1. The question is compared with every FAQ title and alias.
  2. A normalised exact match returns the stored answer.
  3. Otherwise both sides go through the index's English analyzer (stop words, Porter stemming). The answer is returned only when the Dice overlap is ≥ 0.6 **and** the FAQ covers ≥ 75% of the query's terms, so a one-keyword FAQ cannot capture longer questions that merely contain its keyword.
  4. Anything else returns `confident: false` plus BM25 hits, so the agent never gets a wrong answer stated as fact.

## Language and libraries

| Need | Go | Rust |
|---|---|---|
| MCP SDK | [modelcontextprotocol/go-sdk](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp) v1.8 (official, typed tools with schema inference, stdio + streamable HTTP, in-memory transports for tests) | [rmcp](https://docs.rs/crate/rmcp) 3.x (official) |
| Full-text search | [bleve v2](https://github.com/blevesearch/bleve) (pure Go, BM25 since v2.5, mem-only index) | tantivy (faster, larger build) |
| YAML round-trip | `gopkg.in/yaml.v3` Node API keeps key order and unknown keys | serde_yaml (deprecated) / alternatives |
| File watching | fsnotify | notify |

**Go was chosen.**
- Both ecosystems have an official MCP SDK and a solid search engine.
- Go's `yaml.v3` Node round-trip is the simplest way to preserve unknown frontmatter keys, which OKF requires.
- Builds are fast and cross-compile to a single static binary.
- It matches the house default ("Go unless another language fits better").
- tantivy's speed edge doesn't matter at vault scale.

## MCP tool design

- **Small toolset.** Five tools: `kb_ask`, `kb_search`, `kb_get`, `kb_list` and `kb_write`.
- **Descriptions and instructions.** Each tool has a sharp description, and the server instructions say which tool to call when.
- **Compact output.** Hits are path, title, type, description, a one-line snippet and a score. Full bodies come only from `kb_get`, to save context.
- **Read-only annotations.** The read tools are marked read-only and idempotent.
- **Write safety.**
  - `kb_write` refuses paths outside the vault, hidden or `_` folders, non-`.md` files and reserved names.
  - It requires `type` when creating a note and preserves unknown keys.
  - It stamps `generated: {by, at}` and writes atomically.
- **`--read-only` mode.** It removes `kb_write` entirely.
- **Logging.** stdio mode logs only to stderr.

## Teams and ticketing (added 2026-11-22)

The same vault serves a team whose tasks live in Jira.

- **Tickets stay in Jira.** Copying tickets into the vault would go stale, so notes only reference them.
  - The OKF-compatible key `tickets: [PROJ-123]` links a note on purpose. A key in the text counts as a mention.
  - Mentions are found only for project keys listed in `.okvault.yaml`. A generic `[A-Z]+-[0-9]+` pattern matches `UTF-8` and `ISO-8601`.
  - Keys go into an exact-match keyword field, because the English analyzer splits `PROJ-123` into `proj` and `123`.
- **Jira is read-only.** `GET /rest/api/{3|2}/issue/{key}` covers it. Cloud v3 returns descriptions and comments as ADF (JSON), which is flattened to text. Data Center v2 returns a wiki-markup string, kept as is.
  - Auth: basic auth with email and API token on Cloud, and a bearer personal access token on Data Center.
  - Credentials come only from the environment. The config rejects unknown keys and URLs with credentials, so a token can't be committed by mistake.
  - Writing to Jira (comments, transitions) would post on a person's behalf, so it is left out.
- **Trust uses OKF's own keys.**
  - Agents stamp `generated`. People append to `verified` (a list of `{by, at}`), and that moves `stale_after` forward.
  - "Unverified" means the note was generated after its last verification.
  - Agents get no tool to verify, so the signal stays human.
- **Concurrent writers.** Revisions are a short SHA-256 of the file bytes, and `base_revision` gives optimistic concurrency between agents, Obsidian and `git pull`.
- **Git merges.**
  - Generated `index.md` files are rebuilt rather than merged.
  - `log.md` uses `merge=union`, and `okvault index` repairs duplicate date sections.
  - Custom merge drivers were rejected because they need per-clone `.git/config`.

## Sources

- [OKF spec v0.2](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md), [MarkTechPost announcement](https://www.marktechpost.com/2026/06/16/google-cloud-introduces-open-knowledge-format-okf-a-vendor-neutral-markdown-spec-for-giving-ai-agents-curated-context/), [GitBook: What is OKF?](https://www.gitbook.com/blog/what-is-okf-open-knowledge-format), [alexop.dev on OKF](https://alexop.dev/posts/open-knowledge-format-markdown-frontmatter-agent-knowledge/)
- [superops-team/okf](https://github.com/superops-team/okf)
- [Glama: Obsidian MCP servers](https://images.glama.ai/mcp/servers/integrations/obsidian), [Vault Cortex](https://raw.githubusercontent.com/aliasunder/vault-cortex/HEAD/README.md), [cyanheads/obsidian-mcp-server](https://docsearch.algolia.com/mcp/docs/repo/cyanheads/obsidian-mcp-server)
- [Jira Cloud REST v3: get issue](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issues/#api-rest-api-3-issue-issueidorkey-get), [Atlassian Document Format](https://developer.atlassian.com/cloud/jira/platform/apis/document/structure/)
- [Go MCP SDK](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp), [rmcp](https://docs.rs/crate/rmcp), [bleve scoring](https://github.com/blevesearch/bleve/blob/master/docs/scoring.md)
- [Keyword search tied an 8B embedding model](https://towardsdatascience.com/keyword-search-tied-an-8b-embedding-model-at-picking-agent-tools/), [BM25 Wins at Scale](https://huggingface.co/papers/2607.26497.md), [agentmemory LongMemEval](https://raw.githubusercontent.com/rohitg00/agentmemory/HEAD/benchmark/LONGMEMEVAL.md)
