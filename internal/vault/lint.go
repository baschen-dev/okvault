package vault

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/baschen-dev/okvault/internal/config"
	"github.com/baschen-dev/okvault/internal/okf"
)

// Severity of a lint finding.
const (
	Error   = "error"
	Warning = "warning"
)

// Finding is one lint result.
type Finding struct {
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Message  string `json:"message"`
}

// Lint checks OKF conformance (errors) and vault hygiene (warnings).
// Errors are the spec's own conformance rules; warnings are recommendations.
func (v *Vault) Lint() []Finding {
	s := v.Snapshot()
	var out []Finding
	for _, is := range s.Issues {
		sev := Error
		if is.Warning {
			sev = Warning
		}
		out = append(out, Finding{sev, is.Path, is.Message})
	}
	faqQuestions := map[string]string{}
	for _, p := range s.Sorted() {
		c := s.Concepts[p]
		base := path.Base(p)
		if strings.ContainsAny(base, " %#") {
			out = append(out, Finding{Warning, p, "file name has spaces or special characters; use kebab-case so links need no escaping"})
		}
		if c.Description == "" && c.Type != "" { // untyped notes already get one warning
			out = append(out, Finding{Warning, p, "no `description` (recommended; shown in indexes and search hits)"})
		}
		if st := strings.ToLower(c.Status); st != "" && st != "draft" && st != "stable" && st != "deprecated" {
			out = append(out, Finding{Warning, p, fmt.Sprintf("status %q is not draft|stable|deprecated", c.Status)})
		}
		if t, present, ok := c.StaleAfter(); present && !ok {
			out = append(out, Finding{Warning, p, "stale_after is not an ISO 8601 timestamp"})
		} else if ok && time.Now().After(t) {
			out = append(out, Finding{Warning, p, "stale: stale_after " + t.Format(time.RFC3339) + " has passed (run `okvault verify` after checking it)"})
		}
		for _, t := range c.List("tickets") {
			if _, ok := config.NormalizeKey(t); !ok {
				out = append(out, Finding{Warning, p, fmt.Sprintf("tickets: %q is not a ticket key (e.g. PROJ-123)", t)})
			}
		}
		if strings.EqualFold(c.Type, "faq") {
			if strings.TrimSpace(c.Body) == "" {
				out = append(out, Finding{Warning, p, "FAQ has no answer body"})
			}
			for _, q := range append([]string{c.Title}, c.Aliases...) {
				k := strings.ToLower(strings.TrimSpace(q))
				if other, dup := faqQuestions[k]; dup && other != p {
					out = append(out, Finding{Warning, p, fmt.Sprintf("question %q also answered by %s", q, other)})
				}
				faqQuestions[k] = p
			}
		}
		for _, b := range s.Broken[p] {
			out = append(out, Finding{Warning, p, "broken link: " + b})
		}
	}
	// index.md must not carry frontmatter except okf_version at the root (§8).
	for _, d := range allDirs(s) {
		rel := path.Join(d, okf.IndexFile)
		src, err := v.ReadFile(rel)
		if err != nil {
			continue
		}
		if front, _, ok := okf.SplitFrontmatter(src); ok {
			f := strings.TrimSpace(string(front))
			if d != "." || (f != "" && !strings.HasPrefix(f, "okf_version:")) || strings.Count(f, "\n") > 0 {
				out = append(out, Finding{Error, rel, "index.md may only carry `okf_version` frontmatter, at the bundle root"})
			}
		}
	}
	if src, err := v.ReadFile(okf.LogFile); err == nil {
		var prev string
		seen := map[string]bool{}
		for _, l := range strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n") {
			if !strings.HasPrefix(l, "## ") {
				continue
			}
			d := strings.TrimSpace(strings.TrimPrefix(l, "## "))
			if _, err := time.Parse("2006-01-02", d); err != nil {
				out = append(out, Finding{Error, okf.LogFile, fmt.Sprintf("log heading %q is not an ISO date (YYYY-MM-DD)", d)})
				continue
			}
			if prev != "" && d > prev {
				out = append(out, Finding{Error, okf.LogFile, "log entries must be newest first: " + d + " after " + prev + " (run `okvault index` to fix)"})
			}
			if seen[d] {
				out = append(out, Finding{Warning, okf.LogFile, "date " + d + " appears twice, usually after a git merge (run `okvault index` to fix)"})
			}
			seen[d] = true
			prev = d
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return out[i].Severity == Error
		}
		return out[i].Path < out[j].Path
	})
	return out
}
