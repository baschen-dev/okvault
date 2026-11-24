package tracker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Project is a Jira project this machine's credentials can see.
type Project struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// Check is what okvault can reach of a Jira site with this machine's
// credentials. It names where the credentials came from, never their values.
type Check struct {
	URL         string    `json:"url"`
	API         string    `json:"api"`
	Credentials string    `json:"credentials"` // e.g. "JIRA_API_TOKEN + JIRA_EMAIL", or "none"
	User        string    `json:"user,omitempty"`
	Projects    []Project `json:"projects,omitempty"`
	Truncated   bool      `json:"truncated,omitempty"` // more projects than listed
	Problem     string    `json:"problem,omitempty"`
}

// maxProjects bounds the project list (Cloud pages through 50 at a time).
const maxProjects = 1000

var projectKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,19}$`)

// CheckJira signs in to the Jira site at base with the credentials in env and
// lists its projects. api is "2" or "3"; empty picks 3 on *.atlassian.net and
// 2 elsewhere, as okvault does. hc nil uses a 15-second client.
func CheckJira(ctx context.Context, base, api string, env func(string) string, hc *http.Client) Check {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	c := Check{URL: base, API: api, Credentials: "none"}
	u, err := url.Parse(base)
	if base == "" || err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		c.Problem = fmt.Sprintf("%q is not a Jira URL like https://acme.atlassian.net", base)
		return c
	}
	if u.User != nil {
		c.Problem = "the Jira URL must not contain credentials; use JIRA_EMAIL and JIRA_API_TOKEN"
		return c
	}
	cloud := strings.HasSuffix(strings.ToLower(u.Hostname()), ".atlassian.net")
	if c.API == "" {
		c.API = "2"
		if cloud {
			c.API = "3"
		}
	}
	token := strings.TrimSpace(env(EnvToken))
	email := strings.TrimSpace(env(EnvEmail))
	var auth string
	switch {
	case token == "":
		c.Problem = fmt.Sprintf("%s is not set in okvault's environment (with %s on Jira Cloud)", EnvToken, EnvEmail)
		return c
	case email != "":
		c.Credentials = EnvToken + " + " + EnvEmail
		auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(email+":"+token))
	default:
		c.Credentials = EnvToken + " (personal access token)"
		auth = "Bearer " + token
	}
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	get := func(path string, out any) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("Authorization", auth)
		req.Header.Set("Accept", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			return 0, redact(err, auth)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if resp.StatusCode != http.StatusOK {
			return resp.StatusCode, fmt.Errorf("HTTP %d%s", resp.StatusCode, jiraErrors(body))
		}
		return resp.StatusCode, json.Unmarshal(body, out)
	}

	// Who am I, first: with anonymous browsing allowed, bad credentials still
	// list (public) projects without an error.
	var me struct {
		DisplayName string `json:"displayName"`
		Name        string `json:"name"`
	}
	if status, err := get("/rest/api/"+c.API+"/myself", &me); err != nil {
		c.Problem = "signing in failed: " + err.Error()
		switch {
		case (status == http.StatusUnauthorized || status == http.StatusForbidden) && cloud && email == "":
			c.Problem += fmt.Sprintf(". Jira Cloud needs %s with an API token; without it okvault sends the token as a Data Center personal access token", EnvEmail)
		case status == http.StatusUnauthorized:
			c.Problem += fmt.Sprintf(". Check %s (and %s) for this site", EnvToken, EnvEmail)
		case status == http.StatusNotFound:
			c.Problem += fmt.Sprintf(". Is this the site's base URL, and REST API %s right for it?", c.API)
		}
		return c
	}
	c.User = me.DisplayName
	if c.User == "" {
		c.User = me.Name
	}

	if c.API == "3" {
		for start := 0; len(c.Projects) < maxProjects; {
			var page struct {
				Values []Project `json:"values"`
				IsLast bool      `json:"isLast"`
			}
			if _, err := get(fmt.Sprintf("/rest/api/3/project/search?startAt=%d&maxResults=50&orderBy=key", start), &page); err != nil {
				c.Problem = "listing projects failed: " + err.Error()
				return c
			}
			c.Projects = append(c.Projects, page.Values...)
			if page.IsLast || len(page.Values) == 0 {
				break
			}
			start += len(page.Values)
		}
	} else {
		var all []Project
		if _, err := get("/rest/api/2/project", &all); err != nil {
			c.Problem = "listing projects failed: " + err.Error()
			return c
		}
		c.Projects = all
	}
	if len(c.Projects) >= maxProjects {
		c.Projects, c.Truncated = c.Projects[:maxProjects], true
	}
	// okvault only recognises keys like PROJ or OPS_2 in notes.
	kept := c.Projects[:0]
	for _, p := range c.Projects {
		if projectKey.MatchString(p.Key) {
			kept = append(kept, Project{Key: p.Key, Name: p.Name})
		}
	}
	c.Projects = kept
	sort.Slice(c.Projects, func(i, j int) bool { return c.Projects[i].Key < c.Projects[j].Key })
	if len(c.Projects) == 0 && c.Problem == "" {
		c.Problem = "signed in, but these credentials see no projects"
	}
	return c
}
