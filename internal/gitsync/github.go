package gitsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// GitHub is a minimal GitHub REST client for the pull requests of one repository.
type GitHub struct {
	API   string // e.g. https://api.github.com, or https://HOST/api/v3 for GitHub Enterprise Server
	Web   string // e.g. https://github.com
	Owner string
	Repo  string
	Token string
	// TokenSource says where Token came from (never the value).
	TokenSource string
	HTTP        *http.Client
}

// PullRequest is the subset of a GitHub pull request okvault uses.
type PullRequest struct {
	Number   int        `json:"number"`
	URL      string     `json:"html_url"`
	State    string     `json:"state"` // open or closed
	MergedAt *time.Time `json:"merged_at"`
	Head     struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
}

var remoteURL = []*regexp.Regexp{
	regexp.MustCompile(`^https?://(?:[^@/]+@)?([^/:]+)(?::\d+)?/([^/]+)/([^/]+?)(?:\.git)?/?$`),
	regexp.MustCompile(`^ssh://(?:[^@/]+@)?([^/:]+)(?::\d+)?/([^/]+)/([^/]+?)(?:\.git)?/?$`),
	regexp.MustCompile(`^(?:[^@/]+@)?([^/:]+):([^/]+)/([^/]+?)(?:\.git)?/?$`), // git@github.com:o/r.git
}

// parseRemote extracts host, owner and repository from a git remote URL.
func parseRemote(u string) (host, owner, repo string, ok bool) {
	u = strings.TrimSpace(u)
	for _, re := range remoteURL {
		if m := re.FindStringSubmatch(u); m != nil {
			return strings.ToLower(m[1]), m[2], m[3], true
		}
	}
	return "", "", "", false
}

// newGitHub builds a client for a remote URL. Credentials come from GH_TOKEN
// or GITHUB_TOKEN, else from the GitHub CLI (`gh auth token`), never from the vault.
func newGitHub(remote string, env func(string) string) (*GitHub, error) {
	host, owner, name, ok := parseRemote(remote)
	if !ok {
		return nil, fmt.Errorf("remote %q is not a GitHub URL", redactURL(remote))
	}
	g := &GitHub{Owner: owner, Repo: name, Web: "https://" + host, HTTP: &http.Client{Timeout: 20 * time.Second}}
	if host == "github.com" {
		g.API = "https://api.github.com"
	} else {
		g.API = "https://" + host + "/api/v3"
	}
	g.Token, g.TokenSource = tokenFor(host, env)
	return g, nil
}

// tokenFor finds the API token for a GitHub host and says where it came from:
// GH_TOKEN, GITHUB_TOKEN, else the GitHub CLI's login; "none" when there is none.
func tokenFor(host string, env func(string) string) (token, source string) {
	for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if t := strings.TrimSpace(env(k)); t != "" {
			return t, k
		}
	}
	if t := ghAuthToken(host); t != "" {
		return t, "GitHub CLI (gh auth login)"
	}
	return "", "none"
}

// ghAuthToken asks the GitHub CLI for its token for host. A variable so tests
// never reach the real `gh` (and its real login).
var ghAuthToken = func(host string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", host).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func redactURL(u string) string { return credInURL.ReplaceAllString(u, "$1") }

// CompareURL is the page where a person can open the pull request by hand.
func (g *GitHub) CompareURL(base, head string) string {
	esc := func(b string) string { return strings.ReplaceAll(url.PathEscape(b), "%2F", "/") }
	return fmt.Sprintf("%s/%s/%s/compare/%s...%s?expand=1", g.Web, g.Owner, g.Repo, esc(base), esc(head))
}

func (g *GitHub) do(ctx context.Context, method, path string, in, out any) error {
	if g.Token == "" {
		return fmt.Errorf("no GitHub token: set GH_TOKEN or sign in with `gh auth login`")
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(g.API, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "okvault")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("github: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
			Errors  []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		_ = json.Unmarshal(data, &e)
		msg := e.Message
		for _, x := range e.Errors {
			if x.Message != "" {
				msg += ": " + x.Message
			}
		}
		return fmt.Errorf("github: %s %s: %s %s", method, path, resp.Status, msg)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (g *GitHub) pulls(ctx context.Context, head, state string) ([]PullRequest, error) {
	q := url.Values{"head": {g.Owner + ":" + head}, "state": {state}, "per_page": {"30"}}
	var prs []PullRequest
	err := g.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/pulls?%s", g.Owner, g.Repo, q.Encode()), nil, &prs)
	return prs, err
}

// EnsurePR opens a pull request from head into base, or updates the body of
// the one already open.
func (g *GitHub) EnsurePR(ctx context.Context, head, base, title, body string) (*PullRequest, error) {
	open, err := g.pulls(ctx, head, "open")
	if err != nil {
		return nil, err
	}
	if len(open) > 0 {
		pr := open[0]
		var out PullRequest
		if err := g.do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/%s/pulls/%d", g.Owner, g.Repo, pr.Number), map[string]string{"body": body}, &out); err != nil {
			return &pr, err
		}
		return &out, nil
	}
	var out PullRequest
	err = g.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/pulls", g.Owner, g.Repo),
		map[string]any{"title": title, "head": head, "base": base, "body": body, "maintainer_can_modify": true}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// LatestMerged returns the most recently merged pull request from head, or
// nil when one is still open or none was merged.
func (g *GitHub) LatestMerged(ctx context.Context, head string) (*PullRequest, error) {
	prs, err := g.pulls(ctx, head, "all")
	if err != nil {
		return nil, err
	}
	var merged []PullRequest
	for _, p := range prs {
		if p.State == "open" {
			return nil, nil
		}
		if p.MergedAt != nil {
			merged = append(merged, p)
		}
	}
	if len(merged) == 0 {
		return nil, nil
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].MergedAt.After(*merged[j].MergedAt) })
	return &merged[0], nil
}
