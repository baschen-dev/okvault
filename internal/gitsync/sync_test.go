package gitsync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baschen-dev/okvault/internal/config"
	"github.com/baschen-dev/okvault/internal/vault"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// world is a bare origin with a seeded main branch.
func world(t *testing.T, vaultDir string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	origin := filepath.Join(t.TempDir(), "origin.git")
	git(t, filepath.Dir(origin), "init", "-q", "--bare", "-b", "main", origin)
	seed := t.TempDir()
	git(t, seed, "init", "-q", "-b", "main")
	setUser(t, seed, "Seed")
	pre := ""
	if vaultDir != "" {
		pre = vaultDir + "/"
	}
	writeFile(t, seed, ".gitattributes", "* text=auto eol=lf\n"+pre+"log.md merge=union\n")
	writeFile(t, seed, pre+"faq/seed.md", "---\ntype: FAQ\ntitle: Seed?\n---\nseed\n")
	v, err := vault.Open(filepath.Join(seed, filepath.FromSlash(vaultDir)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.RegenerateIndexes(); err != nil {
		t.Fatal(err)
	}
	git(t, seed, "add", "-A")
	git(t, seed, "commit", "-q", "-m", "seed")
	git(t, seed, "remote", "add", "origin", origin)
	git(t, seed, "push", "-q", "origin", "main")
	return origin
}

func setUser(t *testing.T, dir, name string) {
	git(t, dir, "config", "user.name", name)
	git(t, dir, "config", "user.email", strings.ToLower(name)+"@example.com")
	git(t, dir, "config", "core.autocrlf", "false")
}

type member struct {
	dir string
	v   *vault.Vault
	s   *Syncer
}

func clone(t *testing.T, origin, name, vaultDir string, g config.Git, gh *GitHub) *member {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	git(t, filepath.Dir(dir), "clone", "-q", origin, dir)
	setUser(t, dir, name)
	v, err := vault.Open(filepath.Join(dir, filepath.FromSlash(vaultDir)))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse([]byte("git:\n  mode: " + g.Mode + "\n  branch: " + g.Branch + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{Vault: v, Config: func() config.Git { return cfg.Git }, Author: "agent", GitHub: gh, Env: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	v.Git = s
	return &member{dir: dir, v: v, s: s}
}

func (m *member) write(t *testing.T, p, title, body string) *vault.WriteResult {
	t.Helper()
	res, err := m.v.Write(vault.WriteRequest{Path: p, Type: "FAQ", Title: title, Body: body, Mode: "upsert", Author: "claude-code"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestCommitOnlyVaultFiles(t *testing.T) {
	origin := world(t, "vault")
	a := clone(t, origin, "Alice", "vault", config.Git{Mode: "commit"}, nil)
	writeFile(t, a.dir, "src/main.go", "package main\n")
	git(t, a.dir, "add", "src/main.go") // the user's own staged work
	res := a.write(t, "faq/new.md", "New?", "yes")
	if res.Git == nil || res.Git.SHA == "" || res.Git.Error != "" {
		t.Fatalf("git result = %+v", res.Git)
	}
	files := git(t, a.dir, "show", "--name-only", "--format=%s%n%b", "HEAD")
	for _, want := range []string{"docs(kb): add faq/new.md", "Written by claude-code through okvault.", "vault/faq/new.md", "vault/faq/index.md", "vault/log.md"} {
		if !strings.Contains(files, want) {
			t.Errorf("commit lacks %q:\n%s", want, files)
		}
	}
	if strings.Contains(files, "src/main.go") {
		t.Errorf("committed the user's staged file:\n%s", files)
	}
	if st := git(t, a.dir, "status", "--porcelain"); st != "A  src/main.go" {
		t.Errorf("status = %q, want the user's file still staged", st)
	}
	if git(t, a.dir, "rev-list", "--count", "origin/main..HEAD") != "1" {
		t.Error("commit mode must not push")
	}
}

func TestPushRebasesOnTeammate(t *testing.T) {
	origin := world(t, "")
	a := clone(t, origin, "Alice", "", config.Git{Mode: "push"}, nil)
	b := clone(t, origin, "Bob", "", config.Git{Mode: "push"}, nil)
	ctx := context.Background()

	a.write(t, "faq/alpha.md", "Alpha?", "a")
	if st := a.s.Sync(ctx); st.Problem != "" {
		t.Fatal(st.Problem)
	}
	// Bob has not pulled: his note in the same folder conflicts in faq/index.md and log.md
	b.write(t, "faq/beta.md", "Beta?", "b")
	if st := b.s.Sync(ctx); st.Problem != "" || st.Unpushed != 0 {
		t.Fatalf("bob: %+v", st)
	}
	idx := git(t, b.dir, "show", "origin/main:faq/index.md")
	for _, want := range []string{"Alpha?", "Beta?", "Seed?"} {
		if !strings.Contains(idx, want) {
			t.Errorf("index on origin lacks %s:\n%s", want, idx)
		}
	}
	logMd := git(t, b.dir, "show", "origin/main:log.md")
	if !strings.Contains(logMd, "alpha.md") || !strings.Contains(logMd, "beta.md") {
		t.Errorf("log lost an entry:\n%s", logMd)
	}
	if git(t, b.dir, "status", "--porcelain") != "" {
		t.Errorf("bob's tree is dirty: %s", git(t, b.dir, "status", "--porcelain"))
	}
	// Alice gets Bob's note on her next sync
	if st := a.s.Sync(ctx); st.Problem != "" {
		t.Fatal(st.Problem)
	}
	if _, ok := a.v.Snapshot().Concepts["faq/beta.md"]; !ok {
		t.Error("alice's vault does not see beta.md after sync")
	}
}

func TestRealConflictLeavesCommits(t *testing.T) {
	origin := world(t, "")
	a := clone(t, origin, "Alice", "", config.Git{Mode: "push"}, nil)
	b := clone(t, origin, "Bob", "", config.Git{Mode: "push"}, nil)
	ctx := context.Background()
	b.write(t, "faq/seed.md", "Seed?", "bob's answer")
	if st := b.s.Sync(ctx); st.Problem != "" {
		t.Fatal(st.Problem)
	}
	a.write(t, "faq/seed.md", "Seed?", "alice's answer")
	mine := git(t, a.dir, "rev-parse", "HEAD")
	st := a.s.Sync(ctx)
	if !strings.Contains(st.Problem, "faq/seed.md") || !strings.Contains(st.Problem, "conflict") {
		t.Fatalf("problem = %q", st.Problem)
	}
	if git(t, a.dir, "rev-parse", "HEAD") != mine || !strings.Contains(readFile(t, a.dir, "faq/seed.md"), "alice's answer") {
		t.Error("alice's commit was not left as it was")
	}
	if a.s.rebasing(ctx) {
		t.Error("rebase left in progress")
	}
}

func TestDirtyEditIsNotStashedAway(t *testing.T) {
	origin := world(t, "")
	a := clone(t, origin, "Alice", "", config.Git{Mode: "push"}, nil)
	b := clone(t, origin, "Bob", "", config.Git{Mode: "push"}, nil)
	ctx := context.Background()
	b.write(t, "faq/seed.md", "Seed?", "bob's answer")
	b.s.Sync(ctx)
	writeFile(t, a.dir, "faq/seed.md", "---\ntype: FAQ\ntitle: Seed?\n---\ntyping in obsidian\n") // not committed
	a.write(t, "faq/other.md", "Other?", "o")
	st := a.s.Sync(ctx)
	if !strings.Contains(st.Problem, "uncommitted edits to faq/seed.md") {
		t.Fatalf("problem = %q", st.Problem)
	}
	if !strings.Contains(readFile(t, a.dir, "faq/seed.md"), "typing in obsidian") {
		t.Error("the uncommitted edit was lost")
	}
}

func TestCommitEdits(t *testing.T) {
	origin := world(t, "")
	a := clone(t, origin, "Alice", "", config.Git{Mode: "commit"}, nil)
	writeFile(t, a.dir, "faq/typed.md", "---\ntype: FAQ\ntitle: Typed in Obsidian?\n---\nyes\n")
	sha, err := a.s.CommitEdits(context.Background())
	if err != nil || sha == "" {
		t.Fatalf("sha=%q err=%v", sha, err)
	}
	files := git(t, a.dir, "show", "--name-only", "--format=%s", "HEAD")
	if !strings.Contains(files, "docs(kb): edit faq/typed.md") || !strings.Contains(files, "faq/index.md") {
		t.Errorf("commit:\n%s", files)
	}
	if git(t, a.dir, "status", "--porcelain") != "" {
		t.Error("tree still dirty")
	}
}

// fakeGitHub serves the pull request endpoints okvault uses.
type fakeGitHub struct {
	mu    sync.Mutex
	prs   []PullRequest
	posts int
	patch int
	body  string
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer tok" {
		http.Error(w, `{"message":"Bad credentials"}`, 401)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls":
		var out []PullRequest
		for _, p := range f.prs {
			if r.URL.Query().Get("head") == "o:"+p.Head.Ref && (r.URL.Query().Get("state") == "all" || r.URL.Query().Get("state") == p.State) {
				out = append(out, p)
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/pulls":
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.posts++
		pr := PullRequest{Number: len(f.prs) + 1, State: "open"}
		pr.URL = "https://github.com/o/r/pull/" + string(rune('0'+pr.Number))
		pr.Head.Ref = in["head"].(string)
		f.body = in["body"].(string)
		f.prs = append(f.prs, pr)
		_ = json.NewEncoder(w).Encode(pr)
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/repos/o/r/pulls/"):
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.patch++
		f.body = in["body"]
		_ = json.NewEncoder(w).Encode(f.prs[len(f.prs)-1])
	default:
		http.Error(w, `{"message":"Not Found"}`, 404)
	}
}

func TestPRMode(t *testing.T) {
	origin := world(t, "")
	fake := &fakeGitHub{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	gh := &GitHub{API: srv.URL, Web: "https://github.com", Owner: "o", Repo: "r", Token: "tok", HTTP: srv.Client()}
	a := clone(t, origin, "Alice Smith", "", config.Git{Mode: "pr"}, gh)
	b := clone(t, origin, "Bob", "", config.Git{Mode: "push"}, nil)
	ctx := context.Background()

	a.write(t, "faq/one.md", "One?", "1")
	if cur := git(t, a.dir, "branch", "--show-current"); cur != "okvault/alice-smith" {
		t.Fatalf("branch = %q", cur)
	}
	st := a.s.Sync(ctx)
	if st.Problem != "" || st.PR == "" || fake.posts != 1 {
		t.Fatalf("status %+v posts=%d", st, fake.posts)
	}
	if git(t, a.dir, "rev-list", "--count", "origin/main..origin/okvault/alice-smith") != "1" {
		t.Error("branch not pushed with one commit")
	}
	if git(t, a.dir, "rev-list", "--count", "origin/main") != "1" {
		t.Error("pr mode pushed to main")
	}

	// a teammate pushes to main meanwhile; the branch is rebased and the PR body updated
	b.write(t, "faq/bob.md", "Bob?", "b")
	b.s.Sync(ctx)
	a.write(t, "faq/two.md", "Two?", "2")
	if st := a.s.Sync(ctx); st.Problem != "" || fake.posts != 1 || fake.patch != 1 {
		t.Fatalf("status %+v posts=%d patch=%d", st, fake.posts, fake.patch)
	}
	if !isAncestorT(t, a.dir, "origin/main", "origin/okvault/alice-smith") {
		t.Error("branch not rebased on main")
	}
	if !strings.Contains(fake.body, "add faq/one.md") || !strings.Contains(fake.body, "add faq/two.md") {
		t.Errorf("pr body:\n%s", fake.body)
	}

	// the PR is squash-merged on GitHub
	head := git(t, a.dir, "rev-parse", "origin/okvault/alice-smith")
	git(t, b.dir, "fetch", "-q", "origin")
	git(t, b.dir, "merge", "-q", "--squash", "origin/okvault/alice-smith")
	git(t, b.dir, "commit", "-q", "-m", "docs(kb): knowledge updates (#1)")
	git(t, b.dir, "push", "-q", "origin", "main")
	now := time.Now()
	fake.mu.Lock()
	fake.prs[0].State, fake.prs[0].MergedAt, fake.prs[0].Head.SHA = "closed", &now, head
	fake.mu.Unlock()

	a.write(t, "faq/three.md", "Three?", "3")
	st = a.s.Sync(ctx)
	if st.Problem != "" || fake.posts != 2 {
		t.Fatalf("after merge: %+v posts=%d", st, fake.posts)
	}
	if n := git(t, a.dir, "rev-list", "--count", "origin/main..origin/okvault/alice-smith"); n != "1" {
		t.Errorf("new PR should carry only the new commit, has %s:\n%s", n, git(t, a.dir, "log", "--oneline", "origin/main..origin/okvault/alice-smith"))
	}
	if strings.Contains(fake.body, "one.md") || !strings.Contains(fake.body, "three.md") {
		t.Errorf("second pr body:\n%s", fake.body)
	}
}

func TestPRModeRefusesForeignPush(t *testing.T) {
	origin := world(t, "")
	gh := &GitHub{Owner: "o", Repo: "r", Web: "https://github.com"} // no token: compare URL only
	a := clone(t, origin, "Alice", "", config.Git{Mode: "pr"}, gh)
	ctx := context.Background()
	a.write(t, "faq/one.md", "One?", "1")
	st := a.s.Sync(ctx)
	if !strings.Contains(st.Problem, "no GitHub token") || !strings.Contains(st.PR, "/compare/main...okvault/alice") {
		t.Fatalf("status %+v", st)
	}
	// someone pushes a fix onto alice's branch
	other := filepath.Join(t.TempDir(), "x")
	git(t, filepath.Dir(other), "clone", "-q", "-b", "okvault/alice", origin, other)
	setUser(t, other, "Carol")
	writeFile(t, other, "faq/one.md", "---\ntype: FAQ\ntitle: One?\n---\nfixed\n")
	git(t, other, "commit", "-q", "-am", "fix")
	git(t, other, "push", "-q", "origin", "okvault/alice")
	a.write(t, "faq/two.md", "Two?", "2")
	st = a.s.Sync(ctx)
	if !strings.Contains(st.Problem, "did not push") {
		t.Fatalf("problem = %q", st.Problem)
	}
}

func isAncestorT(t *testing.T, dir, a, b string) bool {
	cmd := exec.Command("git", "-C", dir, "merge-base", "--is-ancestor", a, b)
	return cmd.Run() == nil
}

func TestParseRemote(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/abaschen/obsidian-vault.git": "github.com abaschen obsidian-vault",
		"https://x:tok@github.com/o/r":                   "github.com o r",
		"git@github.com:o/r.git":                         "github.com o r",
		"ssh://git@ghe.acme.com:22/team/kb.git":          "ghe.acme.com team kb",
	} {
		h, o, r, ok := parseRemote(in)
		if got := h + " " + o + " " + r; !ok || got != want {
			t.Errorf("%s -> %q", in, got)
		}
	}
	if _, _, _, ok := parseRemote("/tmp/origin.git"); ok {
		t.Error("a path is not a GitHub remote")
	}
}
