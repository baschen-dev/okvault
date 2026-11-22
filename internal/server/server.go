// Package server exposes a vault over the Model Context Protocol.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/baschen-dev/okvault/internal/config"
	"github.com/baschen-dev/okvault/internal/search"
	"github.com/baschen-dev/okvault/internal/tracker"
	"github.com/baschen-dev/okvault/internal/vault"
)

// Instructions are sent to the client at initialisation.
const Instructions = `okvault serves a knowledge vault: Markdown notes in Open Knowledge Format (OKF v0.2), also an Obsidian vault.

Lookup order:
1. kb_ask for a question ("how do we ...?"). It returns a stored FAQ answer when it is confident, else related notes.
2. kb_search for keywords; filter by type, tags or dir. Hits are compact (path, title, description, snippet).
3. kb_get to read one note in full, with its outgoing links and backlinks.
kb_list shows what types, tags and folders exist.

Writing (kb_write): one concept per file, kebab-case .md paths, a required "type" (e.g. FAQ, Playbook, Reference, Decision, Tool).
For an FAQ use type "FAQ", the canonical question as title, other phrasings in aliases, and the answer as body.
Link related notes with relative Markdown links ([Go](../tools/go.md)), not wikilinks. Search before writing to avoid duplicates; prefer updating.
To change an existing note, pass the revision from kb_get as base_revision so a teammate's edit is never overwritten; on a conflict, read it again and merge.

Team work and tickets:
- Starting work on a ticket (e.g. PROJ-123): call kb_ticket first. It returns the ticket from the tracker (when configured) and the notes linked to it, mentioning it, or likely relevant.
- Finishing work on a ticket: record what the team should remember (decisions, gotchas, how-tos, new FAQ answers) with kb_write and tickets: ["PROJ-123"]. Update existing notes rather than adding near-duplicates.
- kb_review lists notes that need a person: stale, unverified agent edits, drafts, broken links. Never set the verified key yourself; people do that with okvault verify.
- The tracker is read-only here: never claim to have updated a ticket.`

// Options configure the server.
type Options struct {
	ReadOnly bool
	Author   string // generated.by for writes, e.g. "claude-code"
	Version  string
	// Env looks up tracker credentials (os.Getenv in production). Nil means
	// no credentials: kb_ticket still returns the related notes.
	Env func(string) string
}

// Server binds a vault and its search index to MCP tools.
type Server struct {
	v    *vault.Vault
	ix   *search.Index
	opts Options
	MCP  *mcp.Server

	tmu      sync.Mutex
	tcfg     *config.Config // config the tracker below was built from
	tr       tracker.Tracker
	trReason string
}

// New builds the MCP server. The caller keeps the index fresh (see Refresh).
func New(v *vault.Vault, ix *search.Index, opts Options) *Server {
	if opts.Version == "" {
		opts.Version = "dev"
	}
	if opts.Author == "" {
		opts.Author = "okvault"
	}
	s := &Server{v: v, ix: ix, opts: opts}
	s.MCP = mcp.NewServer(&mcp.Implementation{
		Name: "okvault", Title: "OKF knowledge vault", Version: opts.Version,
	}, &mcp.ServerOptions{Instructions: Instructions})
	s.register()
	return s
}

// Refresh rebuilds the search index from a new snapshot.
func (s *Server) Refresh(snap *vault.Snapshot) {
	if err := s.ix.Rebuild(snap); err != nil {
		slog.Warn("index rebuild failed", "err", err)
	}
}

var ro = &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(bool)}

// roOpen is for read-only tools that reach the issue tracker.
var roOpen = &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: ptr(true)}

func ptr[T any](v T) *T { return &v }

// ---- kb_ask ----

type AskIn struct {
	Question string `json:"question" jsonschema:"the question, in natural language"`
}

// ---- kb_search ----

type SearchIn struct {
	Query          string   `json:"query" jsonschema:"keywords; empty lists notes matching the filters"`
	Type           string   `json:"type,omitempty" jsonschema:"only notes of this OKF type, e.g. FAQ or Playbook"`
	Tags           []string `json:"tags,omitempty" jsonschema:"only notes carrying all these tags"`
	Dir            string   `json:"dir,omitempty" jsonschema:"only notes under this vault folder"`
	Ticket         string   `json:"ticket,omitempty" jsonschema:"only notes linked to or mentioning this ticket key, e.g. PROJ-123"`
	Owner          string   `json:"owner,omitempty" jsonschema:"only notes owned by this person or group"`
	Limit          int      `json:"limit,omitempty" jsonschema:"max hits (default 8, max 50)"`
	WithDeprecated bool     `json:"with_deprecated,omitempty" jsonschema:"include notes with status: deprecated"`
}

type SearchOut struct {
	Hits []search.Hit `json:"hits"`
}

// ---- kb_get ----

type GetIn struct {
	Path string `json:"path" jsonschema:"vault-relative path (e.g. faq/release.md), or a note title"`
}

type GetOut struct {
	Path        string         `json:"path"`
	Title       string         `json:"title"`
	Type        string         `json:"type"`
	Description string         `json:"description,omitempty"`
	Revision    string         `json:"revision" jsonschema:"pass as base_revision to kb_write when updating this note"`
	Owners      []string       `json:"owners,omitempty"`
	Tickets     []TicketLink   `json:"tickets,omitempty"`
	Frontmatter map[string]any `json:"frontmatter"`
	Body        string         `json:"body"`
	Links       []string       `json:"links,omitempty"`
	Backlinks   []string       `json:"backlinks,omitempty"`
	Broken      []string       `json:"broken_links,omitempty"`
}

type TicketLink struct {
	Key string `json:"key"`
	URL string `json:"url,omitempty"`
}

// ---- kb_ticket ----

type TicketIn struct {
	Key string `json:"key" jsonschema:"ticket key, e.g. PROJ-123"`
}

type TicketOut struct {
	Key         string         `json:"key"`
	URL         string         `json:"url,omitempty"`
	Ticket      *tracker.Issue `json:"ticket,omitempty"`
	TrackerNote string         `json:"tracker_note,omitempty" jsonschema:"why the ticket itself could not be fetched"`
	Linked      []Entry        `json:"linked,omitempty" jsonschema:"notes listing this ticket in their tickets frontmatter"`
	Mentioned   []Entry        `json:"mentioned,omitempty" jsonschema:"notes mentioning the key in their text"`
	Suggested   []search.Hit   `json:"suggested,omitempty" jsonschema:"notes matching the ticket summary and labels"`
	Hint        string         `json:"hint"`
}

// ---- kb_review ----

type ReviewIn struct {
	Owner  string `json:"owner,omitempty" jsonschema:"only notes owned by this person or group"`
	Reason string `json:"reason,omitempty" jsonschema:"only one reason: stale, unverified, draft, broken-links or no-owner"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max items (default 25, max 200)"`
}

type ReviewOut struct {
	Total int                `json:"total"`
	Items []vault.ReviewItem `json:"items"`
}

// ---- kb_list ----

type ListIn struct {
	Type string `json:"type,omitempty" jsonschema:"only notes of this type"`
	Tag  string `json:"tag,omitempty" jsonschema:"only notes with this tag"`
	Dir  string `json:"dir,omitempty" jsonschema:"only notes under this folder"`
}

type Entry struct {
	Path        string `json:"path"`
	Title       string `json:"title"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type Count struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type ListOut struct {
	Total   int     `json:"total"`
	Types   []Count `json:"types"`
	Tags    []Count `json:"tags"`
	Folders []Count `json:"folders"`
	Notes   []Entry `json:"notes"`
	Note    string  `json:"note,omitempty"`
}

// ---- kb_write ----

type WriteIn struct {
	Path         string   `json:"path" jsonschema:"vault-relative .md path in kebab-case, e.g. faq/how-to-release.md"`
	Type         string   `json:"type,omitempty" jsonschema:"OKF type; required when creating (FAQ, Playbook, Reference, Decision, Tool ...)"`
	Title        string   `json:"title,omitempty" jsonschema:"display title; for FAQ the canonical question"`
	Description  string   `json:"description,omitempty" jsonschema:"one sentence summary, shown in indexes and search hits"`
	Tags         []string `json:"tags,omitempty" jsonschema:"tags (replaces existing tags when given)"`
	Aliases      []string `json:"aliases,omitempty" jsonschema:"alternative names; for FAQ other phrasings of the question"`
	Status       string   `json:"status,omitempty" jsonschema:"draft, stable or deprecated"`
	Body         string   `json:"body,omitempty" jsonschema:"Markdown body (replaces the existing body when given)"`
	Tickets      []string `json:"tickets,omitempty" jsonschema:"ticket keys this note documents, e.g. [PROJ-123] (replaces existing)"`
	Owners       []string `json:"owners,omitempty" jsonschema:"people or groups who own this note (replaces existing)"`
	BaseRevision string   `json:"base_revision,omitempty" jsonschema:"revision from kb_get; the write fails if the note changed since"`
	Mode         string   `json:"mode,omitempty" jsonschema:"create, update or upsert (default upsert)"`
	Note         string   `json:"note,omitempty" jsonschema:"short change note recorded in log.md"`
}

func (s *Server) register() {
	mcp.AddTool(s.MCP, &mcp.Tool{
		Name:        "kb_ask",
		Title:       "Ask the knowledge base",
		Description: "Fast answer to a natural-language question from the vault's FAQ notes. Returns the stored answer when the question matches confidently; otherwise confident=false plus the most relevant notes to read with kb_get. Try this first.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in AskIn) (*mcp.CallToolResult, *search.Answer, error) {
		if strings.TrimSpace(in.Question) == "" {
			return nil, nil, errors.New("question is required")
		}
		a, err := s.ix.Ask(in.Question)
		return nil, a, err
	})

	mcp.AddTool(s.MCP, &mcp.Tool{
		Name:        "kb_search",
		Title:       "Search the knowledge base",
		Description: "Ranked keyword search (BM25) over note titles, aliases, descriptions, tags and bodies, with optional type/tag/folder filters. Returns compact hits; read a hit with kb_get.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchIn) (*mcp.CallToolResult, SearchOut, error) {
		hits, err := s.ix.Search(search.Query{Text: in.Query, Type: in.Type, Tags: in.Tags, Dir: in.Dir, Ticket: in.Ticket, Owner: in.Owner, Limit: in.Limit, WithDeprecated: in.WithDeprecated})
		return nil, SearchOut{Hits: hits}, err
	})

	mcp.AddTool(s.MCP, &mcp.Tool{
		Name:        "kb_get",
		Title:       "Read a note",
		Description: "Read one note in full: frontmatter, Markdown body, outgoing links and backlinks (notes that link to it).",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetIn) (*mcp.CallToolResult, *GetOut, error) {
		snap := s.v.Snapshot()
		p := s.resolve(snap, in.Path)
		c, ok := snap.Concepts[p]
		if !ok {
			return nil, nil, fmt.Errorf("no note %q; use kb_search to find paths", in.Path)
		}
		var tickets []TicketLink
		for _, k := range snap.NoteTickets[p] {
			tickets = append(tickets, TicketLink{Key: k, URL: snap.Config.BrowseURL(k)})
		}
		return nil, &GetOut{
			Path: p, Title: c.Title, Type: c.Type, Description: c.Description,
			Revision: snap.Revisions[p], Owners: snap.Owners(p), Tickets: tickets,
			Frontmatter: c.Meta(), Body: c.Body,
			Links: snap.Links[p], Backlinks: snap.Backlinks[p], Broken: snap.Broken[p],
		}, nil
	})

	mcp.AddTool(s.MCP, &mcp.Tool{
		Name:        "kb_list",
		Title:       "Browse the knowledge base",
		Description: "Overview of the vault: counts per type, tag and folder, and the notes matching optional type/tag/folder filters (max 200).",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListIn) (*mcp.CallToolResult, ListOut, error) {
		return nil, s.list(in), nil
	})

	mcp.AddTool(s.MCP, &mcp.Tool{
		Name:        "kb_ticket",
		Title:       "Context for a ticket",
		Description: "Everything the vault knows about a tracker ticket (e.g. PROJ-123): the ticket itself from Jira when configured (summary, status, assignee, description, recent comments), notes linked to it or mentioning it, and notes matching its summary. Call this when starting work on a ticket.",
		Annotations: roOpen,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in TicketIn) (*mcp.CallToolResult, *TicketOut, error) {
		return s.ticket(ctx, in.Key)
	})

	mcp.AddTool(s.MCP, &mcp.Tool{
		Name:        "kb_review",
		Title:       "Notes needing review",
		Description: "The team's maintenance queue: notes that are stale (stale_after passed), written by an agent and not verified by a person since, drafts, with broken links, or without an owner. Filter by owner or reason.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ReviewIn) (*mcp.CallToolResult, ReviewOut, error) {
		items := s.v.Snapshot().Review(vault.ReviewQuery{Owner: in.Owner, Reason: in.Reason}, time.Now())
		out := ReviewOut{Total: len(items), Items: items}
		limit := in.Limit
		if limit <= 0 {
			limit = 25
		}
		if limit > 200 {
			limit = 200
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
		}
		if out.Items == nil {
			out.Items = []vault.ReviewItem{}
		}
		return nil, out, nil
	})

	if s.opts.ReadOnly {
		return
	}
	mcp.AddTool(s.MCP, &mcp.Tool{
		Name:        "kb_write",
		Title:       "Write a note",
		Description: "Create or update one OKF note (Markdown + YAML frontmatter). Given fields replace existing ones; unknown frontmatter keys are kept. Refreshes the folder index.md and records the change in log.md. Search first to avoid duplicates.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(bool), OpenWorldHint: new(bool)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in WriteIn) (*mcp.CallToolResult, *vault.WriteResult, error) {
		res, err := s.v.Write(vault.WriteRequest{
			Path: in.Path, Type: in.Type, Title: in.Title, Description: in.Description,
			Tags: in.Tags, Aliases: in.Aliases, Status: in.Status, Body: in.Body,
			Tickets: in.Tickets, Owners: in.Owners, BaseRevision: in.BaseRevision,
			Mode: in.Mode, Note: in.Note, Author: s.opts.Author,
		}, time.Now())
		if err != nil {
			return nil, nil, err
		}
		s.Refresh(s.v.Snapshot())
		return nil, res, nil
	})
}

// tracker returns the tracker for the current config, rebuilding it when
// .okvault.yaml changed. A nil tracker comes with the reason.
func (s *Server) tracker(cfg *config.Config) (tracker.Tracker, string) {
	s.tmu.Lock()
	defer s.tmu.Unlock()
	if s.tcfg != cfg {
		env := s.opts.Env
		if env == nil {
			env = func(string) string { return "" }
		}
		s.tcfg = cfg
		s.tr, s.trReason = nil, ""
		tr, err := tracker.New(cfg, env)
		if err != nil {
			s.trReason = err.Error()
		} else {
			s.tr = tr
		}
	}
	return s.tr, s.trReason
}

// TicketContext gathers what the vault and the tracker know about a ticket.
func (s *Server) TicketContext(ctx context.Context, key string) (*TicketOut, error) {
	_, out, err := s.ticket(ctx, key)
	return out, err
}

func (s *Server) ticket(ctx context.Context, key string) (*mcp.CallToolResult, *TicketOut, error) {
	k, ok := config.NormalizeKey(key)
	if !ok {
		return nil, nil, fmt.Errorf("%q is not a ticket key (e.g. PROJ-123)", key)
	}
	snap := s.v.Snapshot()
	out := &TicketOut{Key: k, URL: snap.Config.BrowseURL(k)}
	listed := map[string]bool{}
	for _, r := range snap.Tickets[k] {
		c := snap.Concepts[r.Path]
		e := Entry{Path: r.Path, Title: c.Title, Type: c.Type, Description: c.Description}
		listed[r.Path] = true
		if r.Linked {
			out.Linked = append(out.Linked, e)
		} else {
			out.Mentioned = append(out.Mentioned, e)
		}
	}
	tr, reason := s.tracker(snap.Config)
	if tr == nil {
		out.TrackerNote = reason
	} else if is, err := tr.Issue(ctx, k); err != nil {
		out.TrackerNote = err.Error()
	} else {
		out.Ticket = is
		if is.URL != "" {
			out.URL = is.URL
		}
		q := strings.TrimSpace(is.Summary + " " + strings.Join(is.Labels, " "))
		if hits, err := s.ix.Search(search.Query{Text: q, Limit: 8}); err == nil {
			for _, h := range hits {
				if !listed[h.Path] && len(out.Suggested) < 5 {
					out.Suggested = append(out.Suggested, h)
				}
			}
		}
	}
	if len(out.Linked)+len(out.Mentioned) == 0 {
		out.Hint = fmt.Sprintf("No note references %s yet. When the work is done, record decisions and learnings with kb_write and tickets: [%q].", k, k)
	} else {
		out.Hint = fmt.Sprintf("Read these notes with kb_get before starting. Afterwards, update them (or add a note with tickets: [%q]) with what changed.", k)
	}
	return nil, out, nil
}

// resolve accepts a path (with or without .md) or a title.
func (s *Server) resolve(snap *vault.Snapshot, p string) string {
	p = strings.TrimPrefix(strings.TrimSpace(strings.ReplaceAll(p, "\\", "/")), "/")
	if _, ok := snap.Concepts[p]; ok {
		return p
	}
	if _, ok := snap.Concepts[p+".md"]; ok {
		return p + ".md"
	}
	lp := strings.ToLower(p)
	for cp, c := range snap.Concepts {
		if strings.ToLower(c.Title) == lp || strings.ToLower(strings.TrimSuffix(path.Base(cp), ".md")) == lp {
			return cp
		}
	}
	return p
}

func (s *Server) list(in ListIn) ListOut {
	snap := s.v.Snapshot()
	types, tags := search.Facets(snap)
	folders := map[string]int{}
	out := ListOut{Total: len(snap.Concepts)}
	dir := strings.Trim(in.Dir, "/")
	for _, p := range snap.Sorted() {
		c := snap.Concepts[p]
		folders[path.Dir(p)]++
		if in.Type != "" && !strings.EqualFold(c.Type, in.Type) {
			continue
		}
		if in.Tag != "" && !containsFold(c.Tags, in.Tag) {
			continue
		}
		if dir != "" && dir != "." && !strings.HasPrefix(p, dir+"/") {
			continue
		}
		if len(out.Notes) == 200 {
			out.Note = "truncated at 200 notes; narrow with filters"
			continue
		}
		out.Notes = append(out.Notes, Entry{Path: p, Title: c.Title, Type: c.Type, Description: c.Description})
	}
	for _, k := range search.TopKeys(types) {
		out.Types = append(out.Types, Count{k, types[k]})
	}
	for _, k := range search.TopKeys(tags) {
		out.Tags = append(out.Tags, Count{k, tags[k]})
	}
	for _, k := range search.TopKeys(folders) {
		out.Folders = append(out.Folders, Count{k, folders[k]})
	}
	return out
}

func containsFold(xs []string, x string) bool {
	for _, s := range xs {
		if strings.EqualFold(s, x) {
			return true
		}
	}
	return false
}
