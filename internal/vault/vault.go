// Package vault loads an Obsidian vault that is also an OKF bundle, keeps an
// in-memory snapshot (concepts, link graph) and performs safe writes.
//
// Nothing derived is stored inside the vault: the snapshot is rebuilt from the
// Markdown files, so the vault stays fully committable to git.
package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/baschen-dev/okvault/internal/config"
	"github.com/baschen-dev/okvault/internal/okf"
)

// TicketRef is a note related to a ticket.
type TicketRef struct {
	Path   string `json:"path"`
	Linked bool   `json:"linked"` // listed in the note's `tickets` frontmatter; false = mentioned in the body
}

// Revision identifies the exact bytes of a note on disk. kb_write accepts it
// as base_revision so two writers cannot silently overwrite each other.
func Revision(src []byte) string {
	h := sha256.Sum256(src)
	return hex.EncodeToString(h[:6])
}

// Issue is a problem found while loading (or linting) a file.
type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
	Warning bool   `json:"warning,omitempty"` // a recommendation, not a load failure
}

// Snapshot is an immutable view of the vault at one point in time.
type Snapshot struct {
	Concepts  map[string]*okf.Concept // by vault-relative path
	Links     map[string][]string     // outgoing, resolved paths
	Backlinks map[string][]string     // incoming
	Broken    map[string][]string     // unresolved targets per source
	Issues    []Issue
	Dirs      map[string]bool // directories containing concepts (vault-relative, "." is root)
	LoadedAt  time.Time

	Config      *config.Config         // team settings from .okvault.yaml (never nil)
	Revisions   map[string]string      // note path -> revision of its bytes on disk
	Tickets     map[string][]TicketRef // ticket key -> related notes, linked first
	NoteTickets map[string][]string    // note path -> ticket keys (linked and mentioned)
}

// Owners returns a note's owners: its `owners` frontmatter, else the folder
// default from .okvault.yaml.
func (s *Snapshot) Owners(p string) []string {
	if c, ok := s.Concepts[p]; ok {
		if o := c.List("owners"); len(o) > 0 {
			return o
		}
	}
	return s.Config.OwnersFor(p)
}

// Sorted returns concept paths in lexical order.
func (s *Snapshot) Sorted() []string {
	out := make([]string, 0, len(s.Concepts))
	for p := range s.Concepts {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Vault is a vault root on disk.
type Vault struct {
	Root string // absolute

	mu sync.Mutex // serialises writes and git operations on the work tree
	// Git, when set, records okvault's writes as commits.
	Git     Committer
	touched map[string]bool // files written by the current write (under mu)
	busy    atomic.Bool     // a git operation is rewriting the work tree
	snap    *Snapshot
	smu     sync.RWMutex
}

// skipDir reports directories that are never indexed.
func skipDir(name string) bool {
	if name == ".obsidian" || name == ".git" || name == ".trash" || name == "node_modules" {
		return true
	}
	// hidden folders and "_"-prefixed folders (_templates, _attachments) are not knowledge
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// Open opens a vault root and loads a first snapshot.
func Open(root string) (*Vault, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("vault: %s is not a directory", abs)
	}
	v := &Vault{Root: abs}
	if _, err := v.Reload(); err != nil {
		return nil, err
	}
	return v, nil
}

// Snapshot returns the current snapshot.
func (v *Vault) Snapshot() *Snapshot {
	v.smu.RLock()
	defer v.smu.RUnlock()
	return v.snap
}

// Reload rebuilds the snapshot from disk.
func (v *Vault) Reload() (*Snapshot, error) {
	s := &Snapshot{
		Concepts:  map[string]*okf.Concept{},
		Links:     map[string][]string{},
		Backlinks: map[string][]string{},
		Broken:    map[string][]string{},
		Dirs:      map[string]bool{".": true},
		LoadedAt:  time.Now(),

		Revisions:   map[string]string{},
		Tickets:     map[string][]TicketRef{},
		NoteTickets: map[string][]string{},
	}
	cfg, err := config.Load(v.Root)
	if err != nil {
		// keep serving with the last good config; lint reports the problem
		s.Issues = append(s.Issues, Issue{Path: config.File, Message: strings.TrimPrefix(err.Error(), config.File+": ")})
		if prev := v.Snapshot(); prev != nil {
			cfg = prev.Config
		} else {
			cfg, _ = config.Parse(nil)
		}
	}
	s.Config = cfg
	raw := map[string][]okf.Link{}
	err = filepath.WalkDir(v.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != v.Root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), ".md") || okf.IsReserved(d.Name()) {
			return nil
		}
		rel, _ := filepath.Rel(v.Root, p)
		rel = filepath.ToSlash(rel)
		src, err := os.ReadFile(p)
		if err != nil {
			s.Issues = append(s.Issues, Issue{Path: rel, Message: err.Error()})
			return nil
		}
		c, err := okf.Parse(rel, src)
		switch {
		case errors.Is(err, okf.ErrNoFrontmatter):
			c = okf.Plain(rel, src)
			s.Issues = append(s.Issues, Issue{rel, "no frontmatter: loaded as an untyped note; add `type` to make it an OKF concept", true})
		case err != nil:
			s.Issues = append(s.Issues, Issue{Path: rel, Message: err.Error()})
			return nil
		case c.Type == "":
			s.Issues = append(s.Issues, Issue{rel, "frontmatter has no `type` (required by OKF); loaded as an untyped note", true})
		}
		s.Concepts[rel] = c
		s.Dirs[path.Dir(rel)] = true
		s.Revisions[rel] = Revision(src)
		raw[rel] = okf.ExtractLinks(rel, c.Body)
		s.addTickets(rel, c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Name lookup for wikilinks: base name (no extension) and title, case-insensitive.
	byName := map[string]string{}
	for p, c := range s.Concepts {
		base := strings.ToLower(strings.TrimSuffix(path.Base(p), path.Ext(p)))
		if _, dup := byName[base]; !dup {
			byName[base] = p
		}
		byName[strings.ToLower(strings.TrimSuffix(p, ".md"))] = p
		if t := strings.ToLower(c.Title); t != "" {
			if _, dup := byName[t]; !dup {
				byName[t] = p
			}
		}
	}
	for src, links := range raw {
		seen := map[string]bool{}
		for _, l := range links {
			target := l.Target
			if l.Wiki {
				target = byName[strings.ToLower(strings.TrimSuffix(l.Target, ".md"))]
			}
			if _, ok := s.Concepts[target]; !ok || target == "" {
				if !isReservedPath(target) {
					s.Broken[src] = append(s.Broken[src], l.Target)
				}
				continue
			}
			if seen[target] {
				continue
			}
			seen[target] = true
			s.Links[src] = append(s.Links[src], target)
			s.Backlinks[target] = append(s.Backlinks[target], src)
		}
	}
	for _, m := range []map[string][]string{s.Links, s.Backlinks} {
		for k := range m {
			sort.Strings(m[k])
		}
	}
	for k, refs := range s.Tickets {
		sort.Slice(refs, func(i, j int) bool {
			if refs[i].Linked != refs[j].Linked {
				return refs[i].Linked
			}
			return refs[i].Path < refs[j].Path
		})
		s.Tickets[k] = refs
	}
	sort.Slice(s.Issues, func(i, j int) bool { return s.Issues[i].Path < s.Issues[j].Path })
	v.smu.Lock()
	v.snap = s
	v.smu.Unlock()
	return s, nil
}

func isReservedPath(p string) bool { return p != "" && okf.IsReserved(path.Base(p)) }

// addTickets records the tickets a note lists in `tickets` (linked) and the
// keys of configured projects it mentions outside code (mentioned).
func (s *Snapshot) addTickets(p string, c *okf.Concept) {
	seen := map[string]bool{}
	add := func(k string, linked bool) {
		if seen[k] {
			return
		}
		seen[k] = true
		s.Tickets[k] = append(s.Tickets[k], TicketRef{Path: p, Linked: linked})
		s.NoteTickets[p] = append(s.NoteTickets[p], k)
	}
	for _, t := range c.List("tickets") {
		if k, ok := config.NormalizeKey(t); ok {
			add(k, true)
		}
	}
	text := okf.StripCode(c.Title + "\n" + c.Description + "\n" + c.Body)
	for _, k := range s.Config.TicketKeys(text) {
		add(k, false)
	}
}

// ErrInvalidPath is returned for write targets outside the vault or not allowed.
var ErrInvalidPath = errors.New("vault: invalid path")

// CleanPath validates a vault-relative concept path supplied by a client.
func CleanPath(p string) (string, error) {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	if p == "" || strings.HasPrefix(p, "/") || filepath.IsAbs(p) || strings.Contains(p, ":") {
		return "", fmt.Errorf("%w: must be vault-relative: %q", ErrInvalidPath, p)
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("%w: escapes the vault: %q", ErrInvalidPath, p)
	}
	for _, seg := range strings.Split(c, "/") {
		if strings.HasPrefix(seg, ".") || strings.HasPrefix(seg, "_") {
			return "", fmt.Errorf("%w: hidden segment %q", ErrInvalidPath, seg)
		}
	}
	if !strings.HasSuffix(strings.ToLower(c), ".md") {
		return "", fmt.Errorf("%w: must end in .md: %q", ErrInvalidPath, p)
	}
	if okf.IsReserved(path.Base(c)) {
		return "", fmt.Errorf("%w: %s is reserved by OKF and maintained by okvault", ErrInvalidPath, path.Base(c))
	}
	return c, nil
}

// abs returns the absolute on-disk path for a vault-relative path.
func (v *Vault) abs(rel string) string { return filepath.Join(v.Root, filepath.FromSlash(rel)) }

// ReadFile reads a vault-relative file (any file, used for index.md/log.md).
func (v *Vault) ReadFile(rel string) ([]byte, error) { return os.ReadFile(v.abs(rel)) }

// WriteFile writes atomically (temp file + rename).
func (v *Vault) writeFile(rel string, data []byte) error {
	if v.touched != nil {
		v.touched[rel] = true
	}
	dst := v.abs(rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".okvault-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// WriteRequest describes a concept to create or update.
type WriteRequest struct {
	Path        string
	Type        string
	Title       string
	Description string
	Tags        []string
	Aliases     []string
	Status      string
	Body        string
	Tickets     []string // ticket keys (replaces `tickets` when non-nil)
	Owners      []string // owners (replaces `owners` when non-nil)
	Mode        string   // "create" (fail if exists), "update" (fail if missing), "upsert"
	Author      string   // recorded in generated.by
	Note        string   // log message
	// BaseRevision, when set, must equal the note's current revision (from
	// kb_get); otherwise the write fails with ErrConflict instead of
	// overwriting someone else's change.
	BaseRevision string
}

// WriteResult reports what a write did.
type WriteResult struct {
	Path     string  `json:"path"`
	Created  bool    `json:"created"`
	Revision string  `json:"revision"`        // revision of the written note
	Index    string  `json:"index,omitempty"` // index file regenerated, if any
	Git      *Commit `json:"git,omitempty"`   // the commit recording it, when git is on
}

// ErrConflict means the note changed since the caller read it.
var ErrConflict = errors.New("vault: conflict")

// Write creates or updates a concept, then refreshes the directory index.md
// and records the change in the root log.md.
func (v *Vault) Write(req WriteRequest, now time.Time) (*WriteResult, error) {
	rel, err := CleanPath(req.Path)
	if err != nil {
		return nil, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.Git != nil {
		end, err := v.Git.Begin()
		if err != nil {
			return nil, err
		}
		defer end()
	}
	v.touched = map[string]bool{}
	defer func() { v.touched = nil }()
	res, err := v.write(rel, req, now)
	if err != nil {
		return nil, err
	}
	verb := "update"
	if res.Created {
		verb = "add"
	}
	body := req.Note
	if a := strings.TrimSpace(req.Author); a != "" {
		body = strings.TrimSpace(body + "\n\nWritten by " + a + " through okvault.")
	}
	res.Git = v.commit(fmt.Sprintf("docs(kb): %s %s", verb, rel), body)
	return res, nil
}

func (v *Vault) write(rel string, req WriteRequest, now time.Time) (*WriteResult, error) {
	var err error

	existing, readErr := os.ReadFile(v.abs(rel))
	exists := readErr == nil
	switch req.Mode {
	case "", "upsert":
	case "create":
		if exists {
			return nil, fmt.Errorf("vault: %s already exists (use mode=update)", rel)
		}
	case "update":
		if !exists {
			return nil, fmt.Errorf("vault: %s does not exist (use mode=create)", rel)
		}
	default:
		return nil, fmt.Errorf("vault: unknown mode %q", req.Mode)
	}
	if req.BaseRevision != "" {
		cur := "absent"
		if exists {
			cur = Revision(existing)
		}
		if cur != req.BaseRevision {
			return nil, fmt.Errorf("%w: %s changed since revision %s (now %s); read it again with kb_get, merge, and retry",
				ErrConflict, rel, req.BaseRevision, cur)
		}
	}
	var tickets []string
	for _, t := range req.Tickets {
		k, ok := config.NormalizeKey(t)
		if !ok {
			return nil, fmt.Errorf("vault: %q is not a ticket key (e.g. PROJ-123)", t)
		}
		tickets = append(tickets, k)
	}
	if req.Tickets != nil && tickets == nil {
		tickets = []string{}
	}

	var c *okf.Concept
	if exists {
		c, err = okf.Parse(rel, existing)
		if errors.Is(err, okf.ErrNoFrontmatter) {
			c, err = okf.Plain(rel, existing), nil
		}
		if err != nil {
			return nil, fmt.Errorf("vault: cannot update %s: %w", rel, err)
		}
		if c.Type == "" && strings.TrimSpace(req.Type) == "" {
			return nil, fmt.Errorf("vault: %s has no `type` yet; pass one to update it (OKF)", rel)
		}
	} else {
		if strings.TrimSpace(req.Type) == "" {
			return nil, errors.New("vault: `type` is required to create a concept (OKF)")
		}
		c = okf.New(rel, req.Type)
	}
	set := func(k string, val any, present bool) error {
		if !present {
			return nil
		}
		return c.Set(k, val)
	}
	if err := errors.Join(
		set("type", req.Type, req.Type != ""),
		set("title", req.Title, req.Title != ""),
		set("description", req.Description, req.Description != ""),
		set("tags", req.Tags, req.Tags != nil),
		set("aliases", req.Aliases, req.Aliases != nil),
		set("status", req.Status, req.Status != ""),
		set("tickets", tickets, tickets != nil),
		set("owners", req.Owners, req.Owners != nil),
		c.MarkGenerated(orDefault(req.Author, "okvault"), now),
	); err != nil {
		return nil, err
	}
	if req.Type != "" {
		c.Type = req.Type
	}
	if req.Body != "" {
		c.Body = req.Body
	}
	data, err := c.Marshal()
	if err != nil {
		return nil, err
	}
	if err := v.writeFile(rel, data); err != nil {
		return nil, err
	}

	res := &WriteResult{Path: rel, Created: !exists, Revision: Revision(data)}
	kind := "Update"
	if !exists {
		kind = "Creation"
	}
	title := orDefault(req.Title, orDefault(c.Title, okf.TitleFromPath(rel)))
	msg := fmt.Sprintf("[%s](%s)", title, okf.RelLink(okf.LogFile, rel))
	if req.Note != "" {
		msg += " - " + req.Note
	}
	logSrc, _ := v.ReadFile(okf.LogFile)
	if err := v.writeFile(okf.LogFile, okf.AppendLog(logSrc, now, kind, msg)); err != nil {
		return nil, err
	}
	snap, err := v.Reload()
	if err != nil {
		return nil, err
	}
	if idx, err := v.regenIndex(snap, path.Dir(rel)); err == nil && idx != "" {
		res.Index = idx
	}
	if !exists {
		// a new directory must also appear in its parents' Sections
		for d := path.Dir(path.Dir(rel)); ; d = path.Dir(d) {
			_, _ = v.regenIndex(snap, d)
			if d == "." {
				break
			}
		}
	}
	return res, nil
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

// RegenerateIndexes rewrites every okvault-owned index.md (and creates missing
// ones), and repairs log.md after a merge (see okf.NormalizeLog).
// Hand-written index.md files without the generated marker are left alone.
func (v *Vault) RegenerateIndexes() ([]string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.regenerateIndexes(v.Snapshot())
}

func (v *Vault) regenerateIndexes(snap *Snapshot) ([]string, error) {
	var written []string
	dirs := allDirs(snap)
	for _, d := range dirs {
		p, err := v.regenIndex(snap, d)
		if err != nil {
			return written, err
		}
		if p != "" {
			written = append(written, p)
		}
	}
	if src, err := v.ReadFile(okf.LogFile); err == nil {
		if fixed := okf.NormalizeLog(src); string(fixed) != string(src) {
			if err := v.writeFile(okf.LogFile, fixed); err != nil {
				return written, err
			}
			written = append(written, okf.LogFile)
		}
	}
	return written, nil
}

// allDirs returns every directory holding concepts plus their ancestors.
func allDirs(s *Snapshot) []string {
	set := map[string]bool{".": true}
	for d := range s.Dirs {
		for ; d != "."; d = path.Dir(d) {
			set[d] = true
		}
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

func (v *Vault) regenIndex(s *Snapshot, dir string) (string, error) {
	rel := path.Join(dir, okf.IndexFile)
	if cur, err := v.ReadFile(rel); err == nil && !okf.IsGeneratedIndex(cur) {
		return "", nil // hand-written: leave alone
	}
	var entries []okf.IndexEntry
	subs := map[string]bool{}
	for p, c := range s.Concepts {
		d := path.Dir(p)
		if d == dir {
			entries = append(entries, okf.IndexEntry{Path: p, Title: c.Title, Type: orDefault(c.Type, "Untyped"), Description: c.Description})
			continue
		}
		if dir == "." || strings.HasPrefix(d, dir+"/") {
			restPath := d
			if dir != "." {
				restPath = strings.TrimPrefix(d, dir+"/")
			}
			subs[strings.SplitN(restPath, "/", 2)[0]] = true
		}
	}
	subdirs := make([]string, 0, len(subs))
	for s := range subs {
		subdirs = append(subdirs, s)
	}
	data := okf.RenderIndex(dir, entries, subdirs, dir == ".")
	if cur, err := v.ReadFile(rel); err == nil && string(cur) == string(data) {
		return "", nil
	}
	return rel, v.writeFile(rel, data)
}
