---
type: Reference
title: Search ranking
description: kb_search ranks notes with BM25 over boosted fields, built in memory from the Markdown files at startup, then adjusts the order with the links between notes.
tags: [okvault, search]
aliases: [BM25 ranking, search boosts, link ranking]
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

Title and body also match with one typo, at a fifth of the weight.
A ticket key in the query (`PROJ-123`) also matches notes linked to or
mentioning that ticket exactly, with boost 5; the English analyzer alone
would split it into `proj` and `123`.

## Links between notes

After BM25, okvault looks at how notes link to each other and reorders the
results a little. Both adjustments multiply the score, so they decide between
notes that match about equally well. They never let a well-linked note beat a
clearly better match.

- **Notes others link to.** Each note that links to a result raises its score,
  by 10% × ln(1 + number of linking notes), at most 30%. Five linking notes give
  about 18%. Links from deprecated notes, and a note's links to itself, do not
  count. `index.md` and `log.md` are not notes, so their lists of links count
  for nothing.
- **Notes near a ticket.** When the query contains a ticket key (`PROJ-123`),
  the notes about that ticket, and every note one link away from them in
  either direction, score 25% higher. `kb_ticket` does the same when it suggests
  notes from the ticket's summary.

okvault fetches four times as many results as it returns, at least 20 and at
most 100, before it reorders them. That way a note just past the cut can still
move up. The `score` in each hit is the final score, so it follows the order.

Filters (`type`, `tags`, `dir`, `ticket`, `owner`) restrict results without
changing their order. A search with filters and no keywords lists notes by
path, with no link adjustment.
Notes with `status: deprecated` are left out unless `with_deprecated` is set.

Related: [How does kb_ask decide it has an answer?](../faq/how-does-kb-ask-decide.md)
