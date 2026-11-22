---
type: FAQ
title: How does kb_ask decide it has an answer?
description: kb_ask answers only when the question matches an FAQ title or alias exactly, or overlaps it strongly and is covered by it.
tags: [okvault, faq, search]
aliases:
  - When is kb_ask confident?
  - What is the FAQ confidence threshold?
---

The question is compared with every FAQ question (title and aliases).

1. Same words after lower-casing and dropping punctuation: confident (1.0).
2. Otherwise both sides go through the search index's English analyzer
   (stop words removed, Porter stemming). The answer is confident only when
   - the Dice overlap of the terms is at least 0.6, and
   - at least 75% of the question's terms appear in the FAQ question.

The coverage rule stops a short FAQ such as "What is okvault?" from
answering "What is the okvault license?".

When not confident, `kb_ask` returns `confident: false` with the five best
BM25 hits, so the agent can read them with `kb_get`.

To make a question hit reliably, add the phrasing people actually use to the
note's `aliases`. See [Search ranking](../reference/search-ranking.md).
