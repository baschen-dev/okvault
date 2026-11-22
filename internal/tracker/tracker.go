// Package tracker reads tickets from the team's issue tracker (Jira).
//
// It is read-only on purpose: okvault never posts to the tracker on a
// person's behalf. Credentials come from the environment, never from the vault.
package tracker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/baschen-dev/okvault/internal/config"
)

// Environment variables holding Jira credentials.
const (
	EnvToken = "JIRA_API_TOKEN" // Cloud API token (with JIRA_EMAIL) or Data Center personal access token
	EnvEmail = "JIRA_EMAIL"     // Cloud account email; when empty the token is sent as a bearer PAT
)

// ErrNotConfigured means the vault has no fetchable tracker, or no credentials are set.
var ErrNotConfigured = errors.New("tracker: not configured")

// ErrNotFound means the ticket does not exist or the credentials cannot see it.
var ErrNotFound = errors.New("tracker: ticket not found")

// Issue is a compact view of a ticket.
type Issue struct {
	Key         string    `json:"key"`
	URL         string    `json:"url,omitempty"`
	Summary     string    `json:"summary"`
	Type        string    `json:"type,omitempty"`
	Status      string    `json:"status,omitempty"`
	Category    string    `json:"status_category,omitempty"` // To Do, In Progress, Done
	Priority    string    `json:"priority,omitempty"`
	Assignee    string    `json:"assignee,omitempty"`
	Reporter    string    `json:"reporter,omitempty"`
	Labels      []string  `json:"labels,omitempty"`
	Parent      string    `json:"parent,omitempty"`
	Updated     string    `json:"updated,omitempty"`
	Description string    `json:"description,omitempty"` // plain text, truncated
	Comments    []Comment `json:"recent_comments,omitempty"`
}

// Comment is a ticket comment, as plain text.
type Comment struct {
	Author  string `json:"author"`
	Created string `json:"created"`
	Body    string `json:"body"`
}

// Tracker fetches tickets.
type Tracker interface {
	Issue(ctx context.Context, key string) (*Issue, error)
}

// Limits on what is returned to an agent, to keep its context small.
const (
	maxDescription = 4000
	maxComment     = 800
	keepComments   = 3
)

// New returns the tracker the config describes, with credentials from env
// (os.Getenv in production). It returns ErrNotConfigured, with a reason, when
// the vault has no Jira tracker or no token is set.
func New(cfg *config.Config, env func(string) string) (Tracker, error) {
	if cfg == nil || cfg.Tracker.Kind != "jira" {
		return nil, fmt.Errorf("%w: set tracker.kind: jira in %s", ErrNotConfigured, config.File)
	}
	token := strings.TrimSpace(env(EnvToken))
	if token == "" {
		return nil, fmt.Errorf("%w: %s is not set", ErrNotConfigured, EnvToken)
	}
	j := &Jira{
		base: cfg.Tracker.URL, api: cfg.Tracker.API, cfg: cfg,
		hc:    &http.Client{Timeout: 10 * time.Second},
		cache: map[string]cached{}, ttl: time.Minute,
	}
	if email := strings.TrimSpace(env(EnvEmail)); email != "" {
		j.auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(email+":"+token))
	} else {
		j.auth = "Bearer " + token
	}
	return j, nil
}

// Jira is a read-only Jira Cloud (REST v3) or Data Center (REST v2) client.
type Jira struct {
	base, api, auth string
	cfg             *config.Config
	hc              *http.Client

	mu    sync.Mutex
	cache map[string]cached
	ttl   time.Duration
}

type cached struct {
	at    time.Time
	issue *Issue
}

const fields = "summary,status,issuetype,priority,assignee,reporter,labels,parent,updated,description,comment"

// Issue fetches one ticket, cached for a minute.
func (j *Jira) Issue(ctx context.Context, key string) (*Issue, error) {
	k, ok := config.NormalizeKey(key)
	if !ok {
		return nil, fmt.Errorf("tracker: %q is not a ticket key", key)
	}
	j.mu.Lock()
	if c, ok := j.cache[k]; ok && time.Since(c.at) < j.ttl {
		j.mu.Unlock()
		return c.issue, nil
	}
	j.mu.Unlock()

	u := fmt.Sprintf("%s/rest/api/%s/issue/%s?fields=%s", j.base, j.api, url.PathEscape(k), fields)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", j.auth)
	req.Header.Set("Accept", "application/json")
	res, err := j.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tracker: %s: %w", k, redact(err, j.auth))
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	switch {
	case res.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s (or no permission to see it)", ErrNotFound, k)
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("tracker: %s: access denied (%d); check %s/%s", k, res.StatusCode, EnvEmail, EnvToken)
	case res.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("tracker: %s: HTTP %d%s", k, res.StatusCode, jiraErrors(body))
	}
	is, err := decode(body)
	if err != nil {
		return nil, fmt.Errorf("tracker: %s: %w", k, err)
	}
	is.URL = j.cfg.BrowseURL(is.Key)
	j.mu.Lock()
	j.cache[k] = cached{time.Now(), is}
	j.mu.Unlock()
	return is, nil
}

type named struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

func (n *named) String() string {
	if n == nil {
		return ""
	}
	if n.DisplayName != "" {
		return n.DisplayName
	}
	return n.Name
}

type rawIssue struct {
	Key    string `json:"key"`
	Fields struct {
		Summary   string `json:"summary"`
		IssueType *named `json:"issuetype"`
		Priority  *named `json:"priority"`
		Assignee  *named `json:"assignee"`
		Reporter  *named `json:"reporter"`
		Status    *struct {
			Name     string `json:"name"`
			Category *named `json:"statusCategory"`
		} `json:"status"`
		Labels  []string `json:"labels"`
		Updated string   `json:"updated"`
		Parent  *struct {
			Key string `json:"key"`
		} `json:"parent"`
		Description json.RawMessage `json:"description"`
		Comment     *struct {
			Comments []struct {
				Author  *named          `json:"author"`
				Created string          `json:"created"`
				Body    json.RawMessage `json:"body"`
			} `json:"comments"`
		} `json:"comment"`
	} `json:"fields"`
}

func decode(body []byte) (*Issue, error) {
	var r rawIssue
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	f := r.Fields
	is := &Issue{
		Key: r.Key, Summary: f.Summary, Labels: f.Labels, Updated: f.Updated,
		Type: f.IssueType.String(), Priority: f.Priority.String(),
		Assignee: f.Assignee.String(), Reporter: f.Reporter.String(),
		Description: truncate(Text(f.Description), maxDescription),
	}
	if f.Status != nil {
		is.Status = f.Status.Name
		is.Category = f.Status.Category.String()
	}
	if f.Parent != nil {
		is.Parent = f.Parent.Key
	}
	if f.Comment != nil {
		cs := f.Comment.Comments
		if len(cs) > keepComments {
			cs = cs[len(cs)-keepComments:]
		}
		for _, c := range cs {
			is.Comments = append(is.Comments, Comment{Author: c.Author.String(), Created: c.Created, Body: truncate(Text(c.Body), maxComment)})
		}
	}
	return is, nil
}

// Text converts a Jira rich-text field to plain text. REST v2 returns a
// string (wiki markup, kept as is); REST v3 returns an Atlassian Document
// Format (ADF) tree, which is flattened.
func Text(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var n adfNode
	if json.Unmarshal(raw, &n) != nil {
		return ""
	}
	var b strings.Builder
	n.write(&b, "")
	lines := strings.Split(b.String(), "\n")
	out := lines[:0]
	blank := 0
	for _, l := range lines {
		l = strings.TrimRight(l, " ")
		if l == "" {
			if blank++; blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

type adfNode struct {
	Type    string         `json:"type"`
	Text    string         `json:"text"`
	Attrs   map[string]any `json:"attrs"`
	Content []adfNode      `json:"content"`
}

func (n adfNode) write(b *strings.Builder, prefix string) {
	attr := func(k string) string { s, _ := n.Attrs[k].(string); return s }
	switch n.Type {
	case "text":
		b.WriteString(n.Text)
		return
	case "hardBreak":
		b.WriteString("\n" + prefix)
		return
	case "mention", "emoji", "status", "date":
		b.WriteString(orStr(attr("text"), attr("shortName")))
		return
	case "inlineCard", "blockCard", "embedCard":
		b.WriteString(attr("url"))
		return
	case "rule":
		b.WriteString("\n---\n")
		return
	case "bulletList", "orderedList":
		for i, c := range n.Content {
			marker := "- "
			if n.Type == "orderedList" {
				marker = fmt.Sprintf("%d. ", i+1)
			}
			b.WriteString(prefix + marker)
			c.write(b, prefix+"  ")
		}
		return
	case "listItem":
		for i, c := range n.Content {
			if i > 0 {
				b.WriteString(prefix)
			}
			c.write(b, prefix)
		}
		return
	case "codeBlock":
		b.WriteString("```\n")
		for _, c := range n.Content {
			c.write(b, prefix)
		}
		b.WriteString("\n```\n\n")
		return
	}
	for _, c := range n.Content {
		c.write(b, prefix)
	}
	switch n.Type {
	case "paragraph", "heading", "blockquote", "panel", "tableRow":
		b.WriteString("\n")
		if prefix == "" {
			b.WriteString("\n")
		}
	case "tableCell", "tableHeader":
		b.WriteString(" | ")
	}
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func truncate(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

func jiraErrors(body []byte) string {
	var e struct {
		ErrorMessages []string `json:"errorMessages"`
	}
	if json.Unmarshal(body, &e) == nil && len(e.ErrorMessages) > 0 {
		return ": " + truncate(strings.Join(e.ErrorMessages, "; "), 300)
	}
	return ""
}

// redact keeps the Authorization value out of error messages.
func redact(err error, auth string) error {
	msg := err.Error()
	if i := strings.IndexByte(auth, ' '); i > 0 && strings.Contains(msg, auth[i+1:]) {
		return errors.New(strings.ReplaceAll(msg, auth[i+1:], "REDACTED"))
	}
	return err
}
