---
type: Reference
title: Search ranking
description: kb_search ranks notes with BM25 over boosted fields, built in memory from the Markdown files at startup.
tags: [okvault, search]
aliases: [BM25 ranking, search boosts]
---

# Search ranking

okvault builds an in-memory [bleve](https://github.com/blevesearch/bleve) index
with BM25 scoring when it starts, and rebuilds it when a file changes. Nothing is
written into the vault, so the whole vault stays committable.

| Field | Boost |
|---|---|
| title | 4 |
| aliases | 3 |
| description, tags | 2 |
| body | 1 (plus an exact-phrase bonus) |

Title and body also match with one typo, at a fifth of the weight. Filters
(`type`, `tags`, `dir`) restrict results without changing their order.
Notes with `status: deprecated` are left out unless `with_deprecated` is set.

Related: [How does kb_ask decide it has an answer?](../faq/how-does-kb-ask-decide.md)
