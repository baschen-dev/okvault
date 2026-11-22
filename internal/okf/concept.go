// Package okf reads and writes Open Knowledge Format (OKF v0.2) concept files.
//
// A concept is a Markdown file with YAML frontmatter whose only required key is
// `type`. Unknown keys are preserved on round-trip, as the spec requires.
package okf

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Version is the OKF spec version this package targets.
const Version = "0.2"

// Reserved file names that are not concept documents.
const (
	IndexFile = "index.md"
	LogFile   = "log.md"
)

// IsReserved reports whether a file base name is reserved by OKF.
func IsReserved(base string) bool {
	b := strings.ToLower(base)
	return b == IndexFile || b == LogFile
}

// Concept is a parsed OKF concept document.
type Concept struct {
	Path        string   // vault-relative, forward slashes, e.g. "tools/go.md"
	Type        string   // required
	Title       string   // recommended; derived from file name when empty
	Description string   // recommended, one sentence
	Tags        []string // recommended
	Aliases     []string // Obsidian aliases; for FAQ these are question variants
	Status      string   // draft | stable | deprecated; empty means stable
	Body        string

	meta *yaml.Node // full frontmatter mapping, preserved on write
}

// ErrNoFrontmatter is returned when a document has no frontmatter block.
var ErrNoFrontmatter = errors.New("okf: missing YAML frontmatter")

// SplitFrontmatter separates a leading `---` YAML block from the body.
// It accepts LF and CRLF line endings.
func SplitFrontmatter(src []byte) (front []byte, body []byte, ok bool) {
	s := bytes.TrimPrefix(src, []byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM
	var nl []byte
	switch {
	case bytes.HasPrefix(s, []byte("---\n")):
		nl = []byte("\n")
	case bytes.HasPrefix(s, []byte("---\r\n")):
		nl = []byte("\r\n")
	default:
		return nil, s, false
	}
	rest := s[3+len(nl):]
	// closing delimiter: a line that is exactly "---" (or "...")
	for off := 0; off <= len(rest); {
		end := bytes.IndexByte(rest[off:], '\n')
		var line []byte
		next := len(rest)
		if end >= 0 {
			line = rest[off : off+end]
			next = off + end + 1
		} else {
			line = rest[off:]
		}
		l := bytes.TrimRight(line, "\r")
		if string(l) == "---" || string(l) == "..." {
			return rest[:off], rest[next:], true
		}
		if end < 0 {
			break
		}
		off = next
	}
	return nil, s, false
}

// Parse parses a concept document. path is the vault-relative path.
func Parse(path string, src []byte) (*Concept, error) {
	front, body, ok := SplitFrontmatter(src)
	if !ok {
		return nil, ErrNoFrontmatter
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(front, &doc); err != nil {
		return nil, fmt.Errorf("okf: frontmatter: %w", err)
	}
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		if doc.Content[0].Kind != yaml.MappingNode {
			return nil, errors.New("okf: frontmatter is not a mapping")
		}
		m = doc.Content[0]
	}
	c := &Concept{Path: path, Body: strings.ReplaceAll(string(body), "\r\n", "\n"), meta: m}
	c.Type = scalar(m, "type")
	c.Title = scalar(m, "title")
	c.Description = scalar(m, "description")
	c.Status = scalar(m, "status")
	c.Tags = list(m, "tags")
	c.Aliases = list(m, "aliases")
	if c.Title == "" {
		c.Title = TitleFromPath(path)
	}
	return c, nil
}

// TitleFromPath derives a display title from a file name: "go-modules.md" -> "Go modules".
func TitleFromPath(path string) string {
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, ".md")
	base = strings.NewReplacer("-", " ", "_", " ").Replace(base)
	if base == "" {
		return base
	}
	return strings.ToUpper(base[:1]) + base[1:]
}

// Get returns a raw frontmatter value decoded into a Go value, or nil.
func (c *Concept) Get(key string) any {
	v := lookup(c.meta, key)
	if v == nil {
		return nil
	}
	var out any
	if err := v.Decode(&out); err != nil {
		return nil
	}
	return out
}

// Meta returns all frontmatter keys decoded as a map (for display).
func (c *Concept) Meta() map[string]any {
	out := map[string]any{}
	if c.meta != nil {
		_ = c.meta.Decode(&out)
	}
	return out
}

// Set sets a frontmatter key, keeping the position of an existing key.
// A nil value removes the key.
func (c *Concept) Set(key string, value any) error {
	if c.meta == nil {
		c.meta = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	for i := 0; i+1 < len(c.meta.Content); i += 2 {
		if c.meta.Content[i].Value == key {
			if value == nil {
				c.meta.Content = append(c.meta.Content[:i], c.meta.Content[i+2:]...)
				return nil
			}
			var n yaml.Node
			if err := n.Encode(value); err != nil {
				return err
			}
			c.meta.Content[i+1] = &n
			return nil
		}
	}
	if value == nil {
		return nil
	}
	var n yaml.Node
	if err := n.Encode(value); err != nil {
		return err
	}
	c.meta.Content = append(c.meta.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &n)
	return nil
}

// New creates an empty concept with the given type.
func New(path, typ string) *Concept {
	c := &Concept{Path: path, meta: &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}}
	_ = c.Set("type", typ)
	c.Type = typ
	return c
}

// MarkGenerated records agent provenance (`generated: {by, at}`).
func (c *Concept) MarkGenerated(by string, at time.Time) error {
	return c.Set("generated", stampYAML{by, at.Format(time.RFC3339)})
}

// Marshal renders the concept with frontmatter. Line endings are LF.
func (c *Concept) Marshal() ([]byte, error) {
	if strings.TrimSpace(c.Type) == "" {
		return nil, errors.New("okf: concept type is required")
	}
	var buf bytes.Buffer
	buf.WriteString("---\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c.meta); err != nil {
		return nil, err
	}
	_ = enc.Close()
	buf.WriteString("---\n")
	body := strings.TrimLeft(c.Body, "\n")
	if body != "" {
		buf.WriteString("\n")
		buf.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			buf.WriteString("\n")
		}
	}
	return buf.Bytes(), nil
}

func lookup(m *yaml.Node, key string) *yaml.Node {
	if m == nil {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func scalar(m *yaml.Node, key string) string {
	v := lookup(m, key)
	if v == nil || v.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.TrimSpace(v.Value)
}

// list accepts a YAML sequence or a single scalar (Obsidian allows both).
func list(m *yaml.Node, key string) []string {
	v := lookup(m, key)
	if v == nil {
		return nil
	}
	var out []string
	switch v.Kind {
	case yaml.ScalarNode:
		for _, p := range strings.Split(v.Value, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	case yaml.SequenceNode:
		for _, n := range v.Content {
			if n.Kind == yaml.ScalarNode && strings.TrimSpace(n.Value) != "" {
				out = append(out, strings.TrimSpace(n.Value))
			}
		}
	}
	return out
}
