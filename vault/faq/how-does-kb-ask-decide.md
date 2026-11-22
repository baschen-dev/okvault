---
type: FAQ
title: How does kb_ask decide it has an answer?
description: kb_ask compares the question with every FAQ title and alias and answers only above a similarity threshold.
tags: [okvault, faq, search]
aliases:
  - When is kb_ask confident?
  - What is the FAQ confidence threshold?
---

The question and every FAQ question (title and aliases) are lower-cased,
stripped of punctuation and stop words, and lightly stemmed. An exact match
scores 1.0; otherwise the score is the Dice overlap of the remaining words.

At 0.6 or more, `kb_ask` returns the stored answer (`confident: true`). Below
that it returns `confident: false` with the five best BM25 hits, so the agent
can read them with `kb_get`.

To make a question hit reliably, add the phrasing people actually use to the
note's `aliases`. See [Search ranking](../reference/search-ranking.md).
