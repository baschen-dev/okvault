package gitsync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baschen-dev/okvault/internal/config"
)

// noGH stands in for the GitHub CLI so tests never pick up this machine's login.
func noGH(t *testing.T, token string) {
	t.Helper()
	old := ghAuthToken
	ghAuthToken = func(string) string { return token }
	t.Cleanup(func() { ghAuthToken = old })
}

func fakeGitHubAPI(t *testing.T, token string, push bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/user":
			fmt.Fprint(w, `{"login":"ada"}`)
		case "/repos/acme/kb":
			fmt.Fprintf(w, `{"permissions":{"push":%v}}`, push)
		default:
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckGitHubAPI(t *testing.T) {
	noGH(t, "")
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "remote", "add", "origin", "https://ada:leaked@github.com/acme/kb.git")
	ctx := context.Background()
	const tok = "ghp_fake_token"
	api := fakeGitHubAPI(t, tok, true)

	c := CheckGitHub(ctx, dir, CheckOptions{Env: func(k string) string { return map[string]string{"GH_TOKEN": tok}[k] }, API: api.URL})
	if c.Repo != "acme/kb" || c.Token != "GH_TOKEN" || c.User != "ada" || c.CanPush == nil || !*c.CanPush || c.APIProblem != "" {
		t.Fatalf("check: %+v", c)
	}
	b, _ := json.Marshal(c)
	if strings.Contains(string(b), tok) || strings.Contains(string(b), "leaked") {
		t.Errorf("a secret leaked: %s", b)
	}

	// No token anywhere.
	c = CheckGitHub(ctx, dir, CheckOptions{API: api.URL})
	if c.Token != "none" || !strings.Contains(c.APIProblem, "no GitHub token") {
		t.Errorf("no token: %+v", c)
	}
	// The GitHub CLI's login, and a token that cannot push.
	noGH(t, tok)
	c = CheckGitHub(ctx, dir, CheckOptions{API: fakeGitHubAPI(t, tok, false).URL})
	if !strings.HasPrefix(c.Token, "GitHub CLI") || c.CanPush == nil || *c.CanPush || !strings.Contains(c.APIProblem, "not push") {
		t.Errorf("read-only: %+v", c)
	}
	// A token GitHub rejects.
	c = CheckGitHub(ctx, dir, CheckOptions{Env: func(string) string { return "wrong" }, API: api.URL})
	if c.Token != "GH_TOKEN" || !strings.Contains(c.APIProblem, "does not work") {
		t.Errorf("bad token: %+v", c)
	}
}

func TestCheckGitHubPushAndProblems(t *testing.T) {
	noGH(t, "")
	ctx := context.Background()
	if c := CheckGitHub(ctx, t.TempDir(), CheckOptions{}); !strings.Contains(c.Problem, "not in a git repository") {
		t.Errorf("no repo: %+v", c)
	}
	lonely := t.TempDir()
	git(t, lonely, "init", "-q", "-b", "main")
	if c := CheckGitHub(ctx, lonely, CheckOptions{}); !strings.Contains(c.Problem, `no remote "origin"`) {
		t.Errorf("no remote: %+v", c)
	}

	// A local bare remote: no GitHub API, but git can push.
	origin := world(t, "")
	m := clone(t, origin, "Alice", "", config.Git{Mode: "push"}, nil)
	c := CheckGitHub(ctx, m.dir, CheckOptions{PushTest: true})
	if c.Push != "ok" || c.Repo != "" || !strings.Contains(c.APIProblem, "not on GitHub") {
		t.Fatalf("local remote: %+v", c)
	}
	if git(t, filepath.Dir(origin), "--git-dir", origin, "branch", "--list", "okvault-connection-check") != "" {
		t.Error("the dry run created a branch")
	}
	// An unreachable remote fails fast, without prompting.
	git(t, m.dir, "remote", "set-url", "origin", "https://127.0.0.1:1/acme/kb.git")
	if c := CheckGitHub(ctx, m.dir, CheckOptions{PushTest: true, API: "http://127.0.0.1:1"}); c.Push == "ok" || c.Push == "" {
		t.Errorf("unreachable: %+v", c)
	}
}
