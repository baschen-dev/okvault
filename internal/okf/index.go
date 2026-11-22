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

// NormalizeLog merges log.md sections that share a date and orders them
// newest first. Git merges of two branches that both logged on the same day
// (with `merge=union`) produce duplicate headings; this repairs them.
// Identical entry lines are kept once. A log with a heading that is not an
// ISO date is returned unchanged, for a person to fix.
func NormalizeLog(src []byte) []byte {
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	var pre []string
	sections := map[string][]string{}
	var order []string
	cur := ""
	for _, l := range lines {
		if strings.HasPrefix(l, "## ") {
			d := strings.TrimSpace(strings.TrimPrefix(l, "## "))
			if _, err := time.Parse("2006-01-02", d); err != nil {
				return src
			}
			if _, ok := sections[d]; !ok {
				order = append(order, d)
				sections[d] = nil
			}
			cur = d
			continue
		}
		if cur == "" {
			pre = append(pre, l)
			continue
		}
		if strings.TrimSpace(l) == "" {
			continue
		}
		dup := false
		for _, e := range sections[cur] {
			if e == l {
				dup = true
				break
			}
		}
		if !dup {
			sections[cur] = append(sections[cur], l)
		}
	}
	if len(order) == 0 {
		return src
	}
	sort.Sort(sort.Reverse(sort.StringSlice(order)))
	var b strings.Builder
	b.WriteString(strings.TrimRight(strings.Join(pre, "\n"), "\n"))
	for _, d := range order {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("## " + d + "\n\n" + strings.Join(sections[d], "\n"))
	}
	return []byte(b.String() + "\n")
}

// MergeLogs combines two versions of log.md (e.g. both sides of a merge
// conflict): entries from both, one section per date, newest first.
func MergeLogs(a, b []byte) []byte {
	s := string(b)
	if i := strings.Index(s, "\n## "); i >= 0 {
		s = s[i+1:]
	} else if !strings.HasPrefix(s, "## ") {
		s = ""
	}
	return NormalizeLog([]byte(strings.TrimRight(string(a), "\n") + "\n\n" + s))
}
