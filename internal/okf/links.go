package okf

import (
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Link is an outgoing reference from a concept.
type Link struct {
	Target string // resolved vault-relative path (markdown links) or bare name (wikilinks)
	Wiki   bool   // true for [[wikilinks]], resolved later by name
}

var (
	mdLink   = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	wikiLink = regexp.MustCompile(`\[\[([^\]|#]+)(?:#[^\]|]*)?(?:\|[^\]]*)?\]\]`)
	fence    = regexp.MustCompile("(?s)```.*?```")
	inline   = regexp.MustCompile("`[^`\\n]*`")
)

// ExtractLinks returns links to other vault documents found in body.
// from is the vault-relative path of the document containing them.
// Both OKF forms are handled: "/abs/path.md" (bundle-relative) and "./rel.md".
func ExtractLinks(from, body string) []Link {
	body = inline.ReplaceAllString(fence.ReplaceAllString(body, ""), "")
	dir := path.Dir(from)
	seen := map[string]bool{}
	var out []Link
	add := func(l Link) {
		k := l.Target
		if l.Wiki {
			k = "[[" + k
		}
		if l.Target != "" && !seen[k] {
			seen[k] = true
			out = append(out, l)
		}
	}
	for _, m := range mdLink.FindAllStringSubmatch(body, -1) {
		t := m[1]
		if strings.Contains(t, "://") || strings.HasPrefix(t, "mailto:") || strings.HasPrefix(t, "#") {
			continue
		}
		if i := strings.IndexAny(t, "#?"); i >= 0 {
			t = t[:i]
		}
		if u, err := url.PathUnescape(t); err == nil {
			t = u
		}
		if !strings.HasSuffix(strings.ToLower(t), ".md") {
			continue
		}
		add(Link{Target: ResolvePath(dir, t)})
	}
	for _, m := range wikiLink.FindAllStringSubmatch(body, -1) {
		add(Link{Target: strings.TrimSpace(m[1]), Wiki: true})
	}
	return out
}

// ResolvePath resolves a link target against the directory of the linking file.
// It returns "" for targets that escape the vault.
func ResolvePath(dir, target string) string {
	var p string
	if strings.HasPrefix(target, "/") {
		p = path.Clean(strings.TrimPrefix(target, "/"))
	} else {
		p = path.Clean(path.Join(dir, target))
	}
	if p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return ""
	}
	return p
}

// RelLink returns a relative link from the document at `from` to `to`
// (both vault-relative), e.g. "../tools/go.md". Relative links resolve the same
// way in Obsidian, GitHub and OKF consumers.
func RelLink(from, to string) string {
	var fromParts []string
	if d := path.Dir(from); d != "." {
		fromParts = strings.Split(d, "/")
	}
	toParts := strings.Split(to, "/")
	i := 0
	for i < len(fromParts) && i < len(toParts)-1 && fromParts[i] == toParts[i] {
		i++
	}
	var b strings.Builder
	if i == len(fromParts) {
		b.WriteString("./")
	}
	for j := i; j < len(fromParts); j++ {
		b.WriteString("../")
	}
	b.WriteString(strings.Join(toParts[i:], "/"))
	return b.String()
}
