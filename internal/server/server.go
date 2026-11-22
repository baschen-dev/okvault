// Package server exposes a vault over the Model Context Protocol.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/baschen-dev/okvault/internal/search"
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
Link related notes with relative Markdown links ([Go](../tools/go.md)), not wikilinks. Search before writing to avoid duplicates; prefer updating.`

// Options configure the server.
type Options struct {
	ReadOnly bool
	Author   string // generated.by for writes, e.g. "claude-code"
	Version  string
}

// Server binds a vault and its search index to MCP tools.
type Server struct {
	v    *vault.Vault
	ix   *search.Index
	opts Options
	MCP  *mcp.Server
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
	Frontmatter map[string]any `json:"frontmatter"`
	Body        string         `json:"body"`
	Links       []string       `json:"links,omitempty"`
	Backlinks   []string       `json:"backlinks,omitempty"`
	Broken      []string       `json:"broken_links,omitempty"`
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
	Path        string   `json:"path" jsonschema:"vault-relative .md path in kebab-case, e.g. faq/how-to-release.md"`
	Type        string   `json:"type,omitempty" jsonschema:"OKF type; required when creating (FAQ, Playbook, Reference, Decision, Tool ...)"`
	Title       string   `json:"title,omitempty" jsonschema:"display title; for FAQ the canonical question"`
	Description string   `json:"description,omitempty" jsonschema:"one sentence summary, shown in indexes and search hits"`
	Tags        []string `json:"tags,omitempty" jsonschema:"tags (replaces existing tags when given)"`
	Aliases     []string `json:"aliases,omitempty" jsonschema:"alternative names; for FAQ other phrasings of the question"`
	Status      string   `json:"status,omitempty" jsonschema:"draft, stable or deprecated"`
	Body        string   `json:"body,omitempty" jsonschema:"Markdown body (replaces the existing body when given)"`
	Mode        string   `json:"mode,omitempty" jsonschema:"create, update or upsert (default upsert)"`
	Note        string   `json:"note,omitempty" jsonschema:"short change note recorded in log.md"`
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
		hits, err := s.ix.Search(search.Query{Text: in.Query, Type: in.Type, Tags: in.Tags, Dir: in.Dir, Limit: in.Limit, WithDeprecated: in.WithDeprecated})
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
		return nil, &GetOut{
			Path: p, Title: c.Title, Type: c.Type, Description: c.Description,
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
			Mode: in.Mode, Note: in.Note, Author: s.opts.Author,
		}, time.Now())
		if err != nil {
			return nil, nil, err
		}
		s.Refresh(s.v.Snapshot())
		return nil, res, nil
	})
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
