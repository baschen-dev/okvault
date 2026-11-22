---
type: FAQ
title: How do I add an FAQ entry?
description: Create a note of type FAQ whose title is the question and whose body is the answer.
tags: [okvault, faq, writing]
aliases:
  - How do I write a FAQ?
  - Add a question and answer to the knowledge base
  - Create a new FAQ note
---

Create a note under `faq/` with `type: FAQ`.

- `title` is the canonical question.
- `aliases` lists other ways people ask it; `kb_ask` matches against all of them.
- The body is the answer. Keep it short and link to the detailed note.

From an agent, call `kb_write` with `mode: create`. From Obsidian, use the
`FAQ` template in `_templates/`. okvault refreshes `faq/index.md` and adds a
line to `log.md` when the agent writes; run `okvault index` after editing by hand.

See [Note format](../conventions/note-format.md).
