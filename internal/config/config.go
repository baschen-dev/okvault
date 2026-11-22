// Package config reads the team settings committed with a vault (.okvault.yaml).
//
// The file holds shared, non-secret settings only: tracker location, project
// keys, folder owners and the review interval. Credentials come from the
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

	keyRe  *regexp.Regexp
	review time.Duration
}

// Tracker points at the team's ticketing system.
type Tracker struct {
	Kind     string   `yaml:"kind"`     // "jira", or "" / "none" for links without fetching
	URL      string   `yaml:"url"`      // e.g. https://acme.atlassian.net
	Projects []string `yaml:"projects"` // project keys, e.g. [PROJ, OPS]; used to spot ticket keys
	API      string   `yaml:"api"`      // Jira REST version "3" or "2"; default 3 on atlassian.net, else 2
	Browse   string   `yaml:"browse"`   // browse URL template with {key}; default {url}/browse/{key}
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
