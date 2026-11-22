package okf

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// GeneratedMarker marks an index.md that okvault owns and may rewrite.
// Hand-written index files (without the marker) are never overwritten.
const GeneratedMarker = "<!-- okvault:generated -->"

// IndexEntry is one concept listed in a directory index.
type IndexEntry struct {
	Path        string
	Title       string
	Type        string
	Description string
}

// RenderIndex renders an OKF §8 index.md for directory dir ("." for the root).
// Concepts are grouped into `# <type>` sections; subdirectories get a Sections list.
func RenderIndex(dir string, entries []IndexEntry, subdirs []string, root bool) []byte {
	var b strings.Builder
	if root {
		fmt.Fprintf(&b, "---\nokf_version: %q\n---\n\n", Version)
	}
	b.WriteString(GeneratedMarker + "\n\n")
	indexPath := path.Join(dir, IndexFile)
	groups := map[string][]IndexEntry{}
	for _, e := range entries {
		groups[e.Type] = append(groups[e.Type], e)
	}
	types := make([]string, 0, len(groups))
	for t := range groups {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		es := groups[t]
		sort.Slice(es, func(i, j int) bool { return strings.ToLower(es[i].Title) < strings.ToLower(es[j].Title) })
		fmt.Fprintf(&b, "# %s\n\n", t)
		for _, e := range es {
			fmt.Fprintf(&b, "* [%s](%s)", e.Title, RelLink(indexPath, e.Path))
			if e.Description != "" {
				fmt.Fprintf(&b, " - %s", oneLine(e.Description))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(subdirs) > 0 {
		sort.Strings(subdirs)
		b.WriteString("# Sections\n\n")
		for _, s := range subdirs {
			fmt.Fprintf(&b, "* [%s](%s)\n", TitleFromPath(s), RelLink(indexPath, path.Join(dir, s, IndexFile)))
		}
		b.WriteString("\n")
	}
	return []byte(strings.TrimRight(b.String(), "\n") + "\n")
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// IsGeneratedIndex reports whether src is an index okvault may overwrite.
func IsGeneratedIndex(src []byte) bool { return strings.Contains(string(src), GeneratedMarker) }

// AppendLog inserts an entry into an OKF §9 log.md (date-grouped, newest first).
// kind is the conventional bold lead word: Creation, Update, Deprecation.
func AppendLog(src []byte, at time.Time, kind, text string) []byte {
	day := "## " + at.Format("2006-01-02")
	entry := fmt.Sprintf("* **%s** %s", kind, oneLine(text))
	if strings.TrimSpace(string(src)) == "" {
		return []byte("# Log\n\n" + day + "\n\n" + entry + "\n")
	}
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, "## ") {
			continue
		}
		out := append([]string{}, lines[:i]...)
		if l == day {
			j := i + 1
			for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
				j++
			}
			out = append(out, lines[i:j]...)
			out = append(out, entry)
			out = append(out, lines[j:]...)
		} else {
			out = append(out, day, "", entry, "")
			out = append(out, lines[i:]...)
		}
		return []byte(strings.Join(out, "\n"))
	}
	return []byte(strings.TrimRight(string(src), "\n") + "\n\n" + day + "\n\n" + entry + "\n")
}
