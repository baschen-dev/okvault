---
type: FAQ
title: How do I resolve a merge conflict in index.md or log.md?
description: Take either side of a generated index.md and run okvault index; log.md merges by union and okvault index tidies it.
tags: [okvault, faq, git, team]
aliases:
  - Git conflict in index.md
  - Merge conflict in the vault log
---

Both files are maintained by okvault, so do not merge them by hand.

- **`index.md`**: accept either side (`git checkout --theirs vault/faq/index.md`),
  then run `okvault index` (or `just index`). It rewrites every generated index
  from the notes. CI fails if an index is out of date.
- **`log.md`**: `.gitattributes` sets `merge=union`, so git keeps both sides'
  entries without a conflict. If both branches logged on the same day the date
  heading appears twice; `okvault index` merges the sections and keeps them
  newest first. `okvault lint` warns until it is fixed.

If the vault lives in its own repository, copy the `log.md merge=union` line
into that repository's `.gitattributes`.
