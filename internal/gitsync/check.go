package gitsync

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// GitHubCheck is what okvault can reach of the vault's GitHub repository with
// this machine's credentials. It names where a token came from, never its value.
type GitHubCheck struct {
	RemoteName string `json:"remote_name"`
	Remote     string `json:"remote,omitempty"` // URL, credentials removed
	Repo       string `json:"repo,omitempty"`   // owner/name, when the remote is on GitHub
	Token      string `json:"token"`            // API token source: GH_TOKEN, GITHUB_TOKEN, GitHub CLI, or none
	User       string `json:"user,omitempty"`   // the token's GitHub login
	CanPush    *bool  `json:"can_push,omitempty"`
	APIProblem string `json:"api_problem,omitempty"` // pull requests (pr mode) will not open
	Push       string `json:"push,omitempty"`        // git push --dry-run: "ok" or what failed; empty when not checked
	Problem    string `json:"problem,omitempty"`     // no repository or no remote
}

// CheckOptions configure CheckGitHub.
type CheckOptions struct {
	Remote   string              // default origin
	Env      func(string) string // nil: no environment
	PushTest bool                // also run git push --dry-run
	API      string              // override the API base URL (tests)
	HTTP     *http.Client
}

// pushCheckRef is the branch name the push dry run targets; nothing is created.
const pushCheckRef = "refs/heads/okvault-connection-check"

// CheckGitHub reports whether okvault can open pull requests for the vault's
// repository (an API token that can push) and push to it (git credentials).
func CheckGitHub(ctx context.Context, vaultRoot string, o CheckOptions) GitHubCheck {
	if o.Remote == "" {
		o.Remote = "origin"
	}
	if o.Env == nil {
		o.Env = func(string) string { return "" }
	}
	c := GitHubCheck{RemoteName: o.Remote, Token: "none"}
	r, err := openRepo(vaultRoot)
	if err != nil {
		c.Problem = "the vault is not in a git repository"
		return c
	}
	url := r.line(ctx, "remote", "get-url", o.Remote)
	if url == "" {
		c.Problem = fmt.Sprintf("the repository has no remote %q", o.Remote)
		return c
	}
	c.Remote = redactURL(url)

	if host, owner, name, ok := parseRemote(url); !ok || strings.HasPrefix(url, "/") || strings.Contains(url, ":\\") || strings.HasPrefix(url, "file:") {
		c.APIProblem = "the remote is not on GitHub, so pull requests (pr mode) cannot be opened; push mode still works"
	} else {
		c.Repo = owner + "/" + name
		g := &GitHub{Owner: owner, Repo: name, HTTP: o.HTTP}
		if g.HTTP == nil {
			g.HTTP = &http.Client{Timeout: 15 * time.Second}
		}
		g.API = o.API
		if g.API == "" {
			g.API = "https://api.github.com"
			if host != "github.com" {
				g.API = "https://" + host + "/api/v3"
			}
		}
		g.Token, c.Token = tokenFor(host, o.Env)
		if g.Token == "" {
			c.APIProblem = "no GitHub token: set GH_TOKEN, or sign in with `gh auth login`; pull requests (pr mode) need one"
		} else {
			var me struct {
				Login string `json:"login"`
			}
			var repo struct {
				Permissions struct {
					Push bool `json:"push"`
				} `json:"permissions"`
			}
			if err := g.do(ctx, http.MethodGet, "/user", nil, &me); err != nil {
				c.APIProblem = "the token does not work: " + err.Error()
			} else {
				c.User = me.Login
				if err := g.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s", owner, name), nil, &repo); err != nil {
					c.APIProblem = fmt.Sprintf("%s cannot see %s: %v", me.Login, c.Repo, err)
				} else {
					push := repo.Permissions.Push
					c.CanPush = &push
					if !push {
						c.APIProblem = fmt.Sprintf("%s can read %s but not push to it, so okvault cannot push its branch or open pull requests", me.Login, c.Repo)
					}
				}
			}
		}
	}

	if o.PushTest {
		c.Push = r.pushCheck(ctx, o.Remote)
	}
	return c
}

// pushCheck runs `git push --dry-run` to a branch name nobody uses: the remote
// asks for credentials as for a real push, and nothing is sent or created.
// Prompts are off, so a missing credential fails instead of waiting.
func (r *repo) pushCheck(ctx context.Context, remote string) string {
	if r.rev(ctx, "HEAD") == "" {
		return "skipped: the repository has no commits yet"
	}
	_, err := r.runEnv(ctx, []string{"GCM_INTERACTIVE=never", "GIT_ASKPASS=", "SSH_ASKPASS="},
		"push", "--dry-run", "--porcelain", remote, "HEAD:"+pushCheckRef)
	if err == nil {
		return "ok"
	}
	msg := redactURL(err.Error())
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "could not read username"), strings.Contains(low, "authentication failed"),
		strings.Contains(low, "terminal prompts disabled"), strings.Contains(low, "invalid username or password"):
		return "git has no credentials for this remote: sign in once with `git push` in a terminal (Git Credential Manager or `gh auth setup-git` stores them), or use an SSH remote with a loaded key"
	case strings.Contains(low, "permission denied (publickey)"), strings.Contains(low, "host key verification failed"):
		return "SSH refused the key: load it in your SSH agent (okvault cannot answer a passphrase prompt)"
	case strings.Contains(low, "permission to") && strings.Contains(low, "denied"), strings.Contains(low, "403"):
		return "signed in, but not allowed to push to this repository"
	case strings.Contains(low, "could not resolve host"), strings.Contains(low, "unable to access"):
		return "the remote cannot be reached (offline?): " + firstLine(msg)
	}
	return "git push --dry-run failed: " + firstLine(msg)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
