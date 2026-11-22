---
type: Convention
title: Note format
description: How every note in this vault is written so that both Obsidian and okvault (OKF v0.2) can read it.
tags: [okf, obsidian, writing]
aliases: [OKF note format, frontmatter rules]
---

# Note format

Every note is one concept: one Markdown file with YAML frontmatter.

## Frontmatter

| Key | Required | Notes |
|---|---|---|
| `type` | yes | Free text, e.g. `FAQ`, `Playbook`, `Reference`, `Decision`, `Tool`, `Convention` |
| `title` | recommended | Derived from the file name when missing |
| `description` | recommended | One sentence; copied into `index.md` and search hits |
| `tags` | recommended | YAML list |
| `aliases` | optional | Obsidian aliases; for an FAQ, other phrasings of the question |
| `status` | optional | `draft`, `stable` (default) or `deprecated`; deprecated notes are hidden from search by default |
| `sources` | optional | OKF provenance: list of `{resource, title, ...}` |
| `stale_after` | optional | ISO 8601 timestamp; `okvault lint` warns once it passes |
| `generated` | set by okvault | `{by, at}` on notes written by an agent |

Unknown keys are kept as they are.

## Files and links

- Kebab-case file names, no spaces: `how-to-release.md`.
- Link with relative Markdown links: `[Go modules](../reference/go-modules.md)`.
  Obsidian is configured to create links in this form (see `.obsidian/app.json`).
  `[[wikilinks]]` are still understood by okvault but do not render on GitHub.
- `index.md` and `log.md` are reserved and maintained by okvault; see [Writing FAQ entries](../faq/how-do-i-add-an-faq.md).
- Folders starting with `_` (`_templates`, `_attachments`) are not indexed.
