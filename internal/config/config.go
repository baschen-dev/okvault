// Package config reads the team settings committed with a vault (.okvault.yaml).
//
// The file holds shared, non-secret settings only: tracker location, project
// keys, folder owners, the review interval and git policy. Credentials come from the
// environment, never from the vault.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// File is the config file name at the vault root.
const File = ".okvault.yaml"

// DefaultReviewInterval is how long a verification stays fresh when the
// config does not say otherwise.
const DefaultReviewInterval = 180 * 24 * time.Hour

// Config is the team configuration of a vault.
type Config struct {
	Tracker Tracker `yaml:"tracker"`
	// Owners maps a vault folder (e.g. "ops" or "ops/runbooks") to its default
	// owners. A note's own `owners` frontmatter wins; otherwise the longest
	// matching folder applies. "." is the whole vault.
	Owners map[string][]string `yaml:"owners"`
	// ReviewInterval is how long a human verification stays fresh, e.g. "180d",
	// "26w" or a Go duration. `okvault verify` sets stale_after from it.
	ReviewInterval string `yaml:"review_interval"`
	// Git sets how okvault records changes in the vault's git repository.
	Git Git `yaml:"git"`

	keyRe  *regexp.Regexp
	review time.Duration
}

// Git modes.
const (
	GitOff    = "off"    // okvault never runs git (default)
	GitCommit = "commit" // commit okvault's writes locally
	GitPush   = "push"   // also rebase on the upstream branch and push to it
	GitPR     = "pr"     // work on a personal branch with one open GitHub pull request
)

// DefaultPullInterval is how often a serving okvault fetches and rebases in
// push and pr modes when the config does not say otherwise.
const DefaultPullInterval = 5 * time.Minute

// Git holds the vault's git settings. They are team policy, so they are
// committed; a machine can still turn git off with `okvault serve --git off`.
type Git struct {
	Mode   string `yaml:"mode"`   // off, commit, push or pr
	Remote string `yaml:"remote"` // default origin
	// Base is the branch pull requests target (pr mode); default the remote's default branch.
	Base string `yaml:"base"`
	// Branch is the working branch in pr mode; default okvault/<git user name>.
	Branch string `yaml:"branch"`
	// PullInterval is how often to fetch, rebase and push while serving (push
	// and pr modes), e.g. "5m"; "0" means only after okvault's own writes.
	PullInterval string `yaml:"pull_interval"`
	// CommitEdits commits changes made outside okvault (in Obsidian, by hand)
	// once the vault has been quiet this long, e.g. "10m". Empty: never.
	CommitEdits string `yaml:"commit_edits"`

	pull, quiet time.Duration
}

// Every returns the periodic sync interval (0: only after writes).
func (g Git) Every() time.Duration { return g.pull }

// Quiet returns how long edits made outside okvault rest before they are
// committed (0: they are not).
func (g Git) Quiet() time.Duration { return g.quiet }

var gitName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

func (g *Git) normalize() error {
	g.Mode = strings.ToLower(strings.TrimSpace(g.Mode))
	switch g.Mode {
	case "", GitOff:
		g.Mode = GitOff
	case GitCommit, GitPush, GitPR:
	default:
		return fmt.Errorf("git.mode %q must be off, commit, push or pr", g.Mode)
	}
	if g.Remote == "" {
		g.Remote = "origin"
	}
	for k, v := range map[string]string{"git.remote": g.Remote, "git.base": g.Base, "git.branch": g.Branch} {
		if v != "" && (!gitName.MatchString(v) || strings.Contains(v, "..") || strings.HasSuffix(v, "/")) {
			return fmt.Errorf("%s %q is not a valid git name", k, v)
		}
	}
	if g.Remote != "" && strings.Contains(g.Remote, "/") {
		return fmt.Errorf("git.remote %q is a remote name like origin, not a URL", g.Remote)
	}
	g.pull = DefaultPullInterval
	if s := strings.TrimSpace(g.PullInterval); s == "0" {
		g.pull = 0
	} else if s != "" {
		d, err := ParseInterval(s)
		if err != nil {
			return fmt.Errorf("git.pull_interval: %w", err)
		}
		g.pull = d
	}
	if s := strings.TrimSpace(g.CommitEdits); s != "" && s != "0" {
		d, err := ParseInterval(s)
		if err != nil {
			return fmt.Errorf("git.commit_edits: %w", err)
		}
		g.quiet = d
	}
	return nil
}

// Tracker points at the team's ticketing system.
type Tracker struct {
	Kind     string   `yaml:"kind"`     // "jira", or "" / "none" for links without fetching
	URL      string   `yaml:"url"`      // e.g. https://acme.atlassian.net
	Projects []string `yaml:"projects"` // project keys, e.g. [PROJ, OPS]; used to spot ticket keys
	API      string   `yaml:"api"`      // Jira REST version "3" or "2"; default 3 on atlassian.net, else 2
	Browse   string   `yaml:"browse"`   // browse URL template with {key}; default {url}/browse/{key}
	// DocLabel is the Jira label a ticket gets once what the team learned on
	// it is documented, e.g. "documented". It drives the default
	// "Missing docs" filter.
	DocLabel string `yaml:"doc_label"`
	// Filters are the ticket lists the ticket workspace and kb_tickets offer,
	// as JQL. Unset: "My tickets", plus "Missing docs" when doc_label is set.
	// A Jira saved filter is `jql: filter = 12345`.
	Filters []Filter `yaml:"filters"`
}

// Filter is a named JQL query listing tickets.
type Filter struct {
	Name string `yaml:"name" json:"name"`
	JQL  string `yaml:"jql" json:"jql"`
}

// Default filter names.
const (
	FilterMine        = "My tickets"
	FilterMissingDocs = "Missing docs"
)

// TicketFilters returns the configured filters, or the defaults when none are
// configured: the token owner's open tickets and, with a doc_label, finished
// tickets that lack it. Both are limited to the configured projects.
func (c *Config) TicketFilters() []Filter {
	if c == nil {
		return nil
	}
	t := c.Tracker
	if len(t.Filters) > 0 {
		return t.Filters
	}
	if t.Kind != "jira" {
		return nil
	}
	scope := ""
	if len(t.Projects) > 0 {
		scope = "project in (" + strings.Join(t.Projects, ", ") + ") AND "
	}
	out := []Filter{{FilterMine, scope + "assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC"}}
	if t.DocLabel != "" {
		l := JQLString(t.DocLabel)
		out = append(out, Filter{FilterMissingDocs, scope + "statusCategory = Done AND (labels is EMPTY OR labels not in (" + l + ")) ORDER BY updated DESC"})
	}
	return out
}

// FilterURL returns the Jira issue search page for a JQL query, or "" without a tracker URL.
func (c *Config) FilterURL(jql string) string {
	if c == nil || c.Tracker.URL == "" || c.Tracker.Kind != "jira" {
		return ""
	}
	return c.Tracker.URL + "/issues/?jql=" + url.QueryEscape(jql)
}

// JQLString quotes s as a JQL string literal.
func JQLString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

var projectKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,19}$`)

// Load reads root/.okvault.yaml. A missing file yields an empty config.
func Load(root string) (*Config, error) {
	src, err := os.ReadFile(filepath.Join(root, File))
	if errors.Is(err, os.ErrNotExist) {
		return Parse(nil)
	}
	if err != nil {
		return nil, err
	}
	c, err := Parse(src)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", File, err)
	}
	return c, nil
}

// Parse parses config YAML. Unknown keys are rejected so that typos and
// misplaced secrets (token:, password:) fail loudly instead of being committed silently.
func Parse(src []byte) (*Config, error) {
	c := &Config{}
	if len(bytes.TrimSpace(src)) > 0 {
		dec := yaml.NewDecoder(bytes.NewReader(src))
		dec.KnownFields(true)
		if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) { // EOF: only comments
			return nil, err
		}
	}
	t := &c.Tracker
	t.Kind = strings.ToLower(strings.TrimSpace(t.Kind))
	switch t.Kind {
	case "", "none", "jira":
	default:
		return nil, fmt.Errorf("tracker.kind %q is not supported (jira or none)", t.Kind)
	}
	if t.URL != "" {
		u, err := url.Parse(t.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, fmt.Errorf("tracker.url %q is not an http(s) URL", t.URL)
		}
		if u.User != nil {
			return nil, errors.New("tracker.url must not contain credentials; use JIRA_EMAIL / JIRA_API_TOKEN")
		}
		t.URL = strings.TrimRight(t.URL, "/")
	}
	if t.Kind == "jira" && t.URL == "" {
		return nil, errors.New("tracker.url is required for kind jira")
	}
	if t.API == "" && t.URL != "" {
		t.API = "2"
		if u, _ := url.Parse(t.URL); strings.HasSuffix(u.Hostname(), ".atlassian.net") {
			t.API = "3"
		}
	}
	if t.API != "" && t.API != "2" && t.API != "3" {
		return nil, fmt.Errorf("tracker.api %q must be 2 or 3", t.API)
	}
	var keys []string
	for i, p := range t.Projects {
		p = strings.ToUpper(strings.TrimSpace(p))
		if !projectKey.MatchString(p) {
			return nil, fmt.Errorf("tracker.projects: %q is not a project key (e.g. PROJ)", p)
		}
		t.Projects[i] = p
		keys = append(keys, regexp.QuoteMeta(p))
	}
	if len(keys) > 0 {
		sort.Sort(sort.Reverse(sort.StringSlice(keys))) // longer keys sharing a prefix first
		c.keyRe = regexp.MustCompile(`\b(?:` + strings.Join(keys, "|") + `)-[1-9][0-9]*\b`)
	}
	t.DocLabel = strings.TrimSpace(t.DocLabel)
	if strings.ContainsAny(t.DocLabel, " \t\n\"") {
		return nil, fmt.Errorf("tracker.doc_label %q: Jira labels have no spaces or quotes", t.DocLabel)
	}
	seen := map[string]bool{}
	for i, f := range t.Filters {
		f.Name, f.JQL = strings.TrimSpace(f.Name), strings.TrimSpace(f.JQL)
		if f.Name == "" || f.JQL == "" {
			return nil, fmt.Errorf("tracker.filters[%d]: each filter needs a name and a jql", i)
		}
		if seen[strings.ToLower(f.Name)] {
			return nil, fmt.Errorf("tracker.filters: %q is listed twice", f.Name)
		}
		seen[strings.ToLower(f.Name)] = true
		t.Filters[i] = f
	}
	if err := c.Git.normalize(); err != nil {
		return nil, err
	}
	c.review = DefaultReviewInterval
	if c.ReviewInterval != "" {
		d, err := ParseInterval(c.ReviewInterval)
		if err != nil {
			return nil, fmt.Errorf("review_interval: %w", err)
		}
		c.review = d
	}
	owners := map[string][]string{}
	for dir, who := range c.Owners {
		d := strings.Trim(filepath.ToSlash(dir), "/")
		if d == "" {
			d = "."
		}
		owners[d] = who
	}
	c.Owners = owners
	return c, nil
}

// ParseInterval accepts "30d", "4w" or any Go duration ("720h").
func ParseInterval(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	for suffix, unit := range map[string]time.Duration{"d": 24 * time.Hour, "w": 7 * 24 * time.Hour} {
		if n, ok := strings.CutSuffix(s, suffix); ok {
			v, err := strconv.Atoi(n)
			if err != nil || v <= 0 {
				return 0, fmt.Errorf("invalid interval %q", s)
			}
			return time.Duration(v) * unit, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid interval %q (use 180d, 26w or 4320h)", s)
	}
	return d, nil
}

// Review returns the review interval.
func (c *Config) Review() time.Duration { return c.review }

// TicketKeys finds ticket keys of the configured projects in text.
// Without configured projects it finds nothing: a generic pattern would match UTF-8 or ISO-8601.
func (c *Config) TicketKeys(text string) []string {
	if c == nil || c.keyRe == nil {
		return nil
	}
	return c.keyRe.FindAllString(text, -1)
}

// NormalizeKey upper-cases a ticket key and reports whether it looks like one (ABC-123).
func NormalizeKey(k string) (string, bool) {
	k = strings.ToUpper(strings.TrimSpace(k))
	i := strings.LastIndexByte(k, '-')
	if i <= 0 || i == len(k)-1 || !projectKey.MatchString(k[:i]) {
		return k, false
	}
	if n, err := strconv.Atoi(k[i+1:]); err != nil || n <= 0 {
		return k, false
	}
	return k, true
}

// BrowseURL returns the human link for a ticket, or "" when no tracker is configured.
func (c *Config) BrowseURL(key string) string {
	if c == nil {
		return ""
	}
	t := c.Tracker
	switch {
	case t.Browse != "":
		return strings.ReplaceAll(t.Browse, "{key}", url.PathEscape(key))
	case t.URL != "":
		return t.URL + "/browse/" + url.PathEscape(key)
	}
	return ""
}

// OwnersFor returns the default owners of a vault-relative note path.
func (c *Config) OwnersFor(notePath string) []string {
	if c == nil {
		return nil
	}
	var best []string
	bestLen := -1
	for dir, who := range c.Owners {
		match, l := dir == ".", 0
		if !match {
			match, l = strings.HasPrefix(notePath, dir+"/"), len(dir)
		}
		if match && l > bestLen {
			best, bestLen = who, l
		}
	}
	return best
}
