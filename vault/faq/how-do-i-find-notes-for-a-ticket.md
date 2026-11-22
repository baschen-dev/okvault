---
type: FAQ
title: How do I find the notes for a ticket?
description: kb_ticket (or okvault ticket KEY) returns the ticket and every note linked to it, mentioning it, or matching its summary.
tags: [okvault, faq, tickets]
aliases:
  - What do we know about this ticket?
  - Which notes are linked to a Jira issue?
  - Find knowledge for a Jira ticket
---

Call `kb_ticket` with the key, e.g. `PROJ-123`, or run `okvault ticket PROJ-123`.

It returns:

- the ticket from Jira (summary, status, assignee, description, last comments)
  when `.okvault.yaml` configures the tracker and `JIRA_API_TOKEN` is set;
- notes that list the ticket in `tickets` (linked) or mention its key (mentioned);
- notes matching the ticket's summary and labels (suggested).

Without Jira credentials you still get the linked and mentioning notes.
`kb_search` also takes a `ticket` filter, and searching for the key finds it.

See [Team workflow](../conventions/team-workflow.md).
