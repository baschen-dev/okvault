package gitsync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baschen-dev/okvault/internal/config"
	"github.com/baschen-dev/okvault/internal/vault"
)

// Two Claude Code sessions run two okvault processes on one checkout. Two
// Vault+Syncer pairs share no Go locks, exactly like two processes.
func TestTwoServersOneCheckout(t *testing.T) {
	origin := world(t, "")
	a := clone(t, origin, "Alice", "", config.Git{Mode: "push"}, nil)
	v2, err := vault.Open(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Parse([]byte("git:\n  mode: push\n"))
	s2, err := New(Options{Vault: v2, Config: func() config.Git { return cfg.Git }, Author: "agent", Env: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	v2.Git = s2
	second := &member{dir: a.dir, v: v2, s: s2}

	ctx := context.Background()
	const n = 6
	var wg sync.WaitGroup
	errs := make(chan error, 4*n)
	for i, m := range []*member{a, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < n; j++ {
				p := fmt.Sprintf("faq/s%d-%d.md", i, j)
				res, err := m.v.Write(vault.WriteRequest{Path: p, Type: "FAQ", Title: fmt.Sprintf("Q %d %d?", i, j), Body: "a", Mode: "create", Author: "agent"}, time.Now())
				if err != nil {
					errs <- err
					continue
				}
				if res.Git == nil || res.Git.SHA == "" {
					errs <- fmt.Errorf("%s not committed: %+v", p, res.Git)
				}
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 3; j++ {
				// two processes pushing at once: one push can lose the race; it is
				// retried on the next sync, which must then succeed (checked below)
				if st := m.s.Sync(ctx); st.Problem != "" && !strings.HasPrefix(st.Problem, "push failed") {
					errs <- fmt.Errorf("sync: %s", st.Problem)
				}
				if _, err := m.s.CommitEdits(ctx); err != nil {
					errs <- fmt.Errorf("commit edits: %w", err)
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if st := a.s.Sync(ctx); st.Problem != "" || st.Unpushed != 0 {
		t.Fatalf("final sync: %+v", st)
	}
	log := git(t, a.dir, "log", "--format=%s", "origin/main")
	for i := 0; i < 2; i++ {
		for j := 0; j < n; j++ {
			if want := fmt.Sprintf("docs(kb): add faq/s%d-%d.md", i, j); !strings.Contains(log, want) {
				t.Errorf("missing commit %q", want)
			}
		}
	}
	if st := git(t, a.dir, "status", "--porcelain"); st != "" {
		t.Errorf("tree left dirty:\n%s", st)
	}
	if _, err := os.Stat(filepath.Join(a.dir, ".git", "okvault.lock")); err == nil {
		t.Error("lock file left behind")
	}
}
