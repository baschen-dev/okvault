package vault

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// IgnoreFile names the vault-root file listing paths okvault leaves out of the
// index, in a subset of .gitignore syntax.
const IgnoreFile = ".vaultignore"

// Ignore is a parsed .vaultignore: gitignore-style rules, the last match wins.
//
// Supported: blank lines and # comments, ! to re-include, a trailing / for
// directories only, a leading / (or any inner /) to anchor the pattern at the
// vault root, *, ? and **. A pattern without a slash matches a name at any
// depth. As in git, a file inside an ignored directory cannot be re-included.
type Ignore struct {
	rules []ignoreRule
}

type ignoreRule struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
}

// ParseIgnore parses .vaultignore text.
func ParseIgnore(src string) *Ignore {
	ig := &Ignore{}
	for _, line := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		line = strings.TrimRight(line, " \t")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := ignoreRule{}
		if strings.HasPrefix(line, "!") {
			r.negate = true
			line = line[1:]
		}
		line = strings.ReplaceAll(line, "\\", "/")
		if strings.HasSuffix(line, "/") {
			r.dirOnly = true
			line = strings.TrimRight(line, "/")
		}
		anchored := strings.Contains(line, "/")
		line = strings.TrimLeft(line, "/")
		if line == "" {
			continue
		}
		prefix := "^(?:.*/)?"
		if anchored {
			prefix = "^"
		}
		re, err := regexp.Compile(prefix + globToRegexp(line) + "$")
		if err != nil {
			continue
		}
		r.re = re
		ig.rules = append(ig.rules, r)
	}
	return ig
}

func globToRegexp(g string) string {
	var b strings.Builder
	for i := 0; i < len(g); i++ {
		switch c := g[i]; {
		case strings.HasPrefix(g[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(g[i:], "/**") && i+3 == len(g):
			b.WriteString("/.*")
			i += 2
		case strings.HasPrefix(g[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}

// LoadIgnore reads root/.vaultignore; a missing file ignores nothing.
func LoadIgnore(root string) (*Ignore, error) {
	src, err := os.ReadFile(filepath.Join(root, IgnoreFile))
	if os.IsNotExist(err) {
		return &Ignore{}, nil
	}
	if err != nil {
		return &Ignore{}, err
	}
	return ParseIgnore(string(src)), nil
}

// Empty reports whether no rule is set.
func (ig *Ignore) Empty() bool { return ig == nil || len(ig.rules) == 0 }

// matchSelf applies the rules to one path, ignoring its parents.
func (ig *Ignore) matchSelf(rel string, dir bool) bool {
	ignored := false
	for _, r := range ig.rules {
		if r.dirOnly && !dir {
			continue
		}
		if r.re.MatchString(rel) {
			ignored = !r.negate
		}
	}
	return ignored
}

// Match reports whether a vault-relative, slash-separated path is ignored,
// itself or through one of its parent directories.
func (ig *Ignore) Match(rel string, dir bool) bool {
	if ig.Empty() {
		return false
	}
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	if rel == "" || rel == "." {
		return false
	}
	segs := strings.Split(rel, "/")
	for i := 1; i < len(segs); i++ {
		if ig.matchSelf(strings.Join(segs[:i], "/"), true) {
			return true
		}
	}
	return ig.matchSelf(rel, dir)
}
