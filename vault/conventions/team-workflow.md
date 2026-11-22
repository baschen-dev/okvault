---
type: Convention
title: Team workflow
description: How a team shares this vault and its tickets - linking notes to tickets, owners, human verification and the review queue.
tags: [okvault, team, tickets]
aliases: [working as a team, ticket workflow]
---

# Team workflow

The vault is shared through git. Tasks live in the ticketing system (Jira);
knowledge lives here. okvault connects the two in both directions.

## Tickets

- A note lists the tickets it documents in frontmatter: `tickets: [PROJ-123]`.
- Ticket keys written in the text ("fixed in PROJ-123") also count, as
  *mentions*, when the project key is listed in `.okvault.yaml`. Keys inside
  code are ignored.
- `kb_ticket PROJ-123` (CLI: `okvault ticket PROJ-123`) returns the ticket
  from Jira, the linked and mentioning notes, and notes that match the
  ticket's summary. An agent calls it when it starts work on a ticket.
- When the work is done, the agent records what the team should remember
  (decision, gotcha, how-to, new FAQ answer) with `kb_write` and `tickets`.
  Jira stays read-only for okvault: it never comments or changes status.

## Owners

- `owners: [alice, team-ops]` on a note, or a folder default in
  `.okvault.yaml` (`owners: {ops: [team-ops]}`); the note wins.
- `kb_search` and `kb_review` filter by owner.

## Trust: generated and verified

- Every agent write stamps `generated: {by, at}`.
- A person who checks a note runs `okvault verify path/to/note.md`. It appends
  `{by, at}` to `verified` (OKF) and sets `stale_after` one review interval
  ahead (`review_interval` in `.okvault.yaml`, default 180 days).
- A note is *unverified* while its last `generated.at` is newer than its last
  verification. Agents cannot verify notes.

## Review queue

`kb_review` (CLI: `okvault review --owner alice`) lists notes that need a person:

| Reason | Meaning |
|---|---|
| `stale` | `stale_after` has passed |
| `unverified` | changed by an agent since the last human check |
| `draft` | `status: draft` |
| `broken-links` | links to notes that do not exist |
| `no-owner` | no owner, when the vault configures owners |

## Concurrent edits

- `kb_get` returns a `revision`. `kb_write` with `base_revision` fails if the
  note changed since (a teammate's edit in Obsidian, a `git pull`), so the
  agent reads it again and merges instead of overwriting.
- Git conflicts in generated files: see
  [How do I resolve a merge conflict in index.md or log.md?](../faq/how-do-i-resolve-a-merge-conflict.md)

## Configuration

`.okvault.yaml` at the vault root is committed and holds no secrets.
Credentials come from the environment of whoever runs okvault:
`JIRA_API_TOKEN`, plus `JIRA_EMAIL` on Jira Cloud (without it the token is
sent as a Data Center personal access token).

```yaml
tracker:
  kind: jira
  url: https://acme.atlassian.net
  projects: [PROJ, OPS]
owners:
  ops: [team-ops]
review_interval: 180d
```

See [Note format](note-format.md) for every frontmatter key.
