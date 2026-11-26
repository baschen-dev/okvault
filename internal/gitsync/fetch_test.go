package gitsync

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/baschen-dev/okvault/internal/config"
)

// holdRefLock takes git's lock on a remote-tracking ref, as a concurrent
// `git fetch` from another okvault on the same checkout does, and returns a
// function that releases it.
func holdRefLock(t *testing.T, m *member, ref string) func() {
	t.Helper()
	p := m.s.r.gitPath(ref + ".lock")
	if err := os.WriteFile(p, []byte("0000000000000000000000000000000000000000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return func() { _ = os.Remove(p) }
}

// Two okvault processes on one checkout fetch at the same time: the one that
// finds origin/main locked must wait for the other, not report "offline?".
func TestFetchWaitsOutConcurrentFetch(t *testing.T) {
	origin := world(t, "")
	a := clone(t, origin, "Alice", "", config.Git{Mode: "push"}, nil)
	b := clone(t, origin, "Bob", "", config.Git{Mode: "push"}, nil)
	ctx := context.Background()
	b.write(t, "faq/bob.md", "Bob?", "b")
	if st := b.s.Sync(ctx); st.Problem != "" {
		t.Fatalf("bob sync: %s", st.Problem)
	}
	want := git(t, b.dir, "rev-parse", "HEAD")

	release := holdRefLock(t, a, "refs/remotes/origin/main")
	go func() {
		time.Sleep(400 * time.Millisecond)
		release()
	}()
	if err := a.s.fetch(ctx); err != nil {
		t.Fatalf("fetch while another fetch held the ref: %v", err)
	}
	if got := git(t, a.dir, "rev-parse", "origin/main"); got != want {
		t.Fatalf("origin/main = %s, want %s", got, want)
	}
}

// A ref that stays locked (a crashed git) still fails, and says why.
func TestFetchGivesUpOnStuckLock(t *testing.T) {
	origin := world(t, "")
	a := clone(t, origin, "Alice", "", config.Git{Mode: "push"}, nil)
	b := clone(t, origin, "Bob", "", config.Git{Mode: "push"}, nil)
	ctx := context.Background()
	b.write(t, "faq/bob.md", "Bob?", "b")
	if st := b.s.Sync(ctx); st.Problem != "" {
		t.Fatalf("bob sync: %s", st.Problem)
	}
	defer holdRefLock(t, a, "refs/remotes/origin/main")()
	start := time.Now()
	err := a.s.fetch(ctx)
	if err == nil {
		t.Fatal("fetch succeeded with origin/main locked")
	}
	if strings.Contains(err.Error(), "offline?") || !strings.Contains(err.Error(), "another git") {
		t.Errorf("error should blame the lock, not the network: %v", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("gave up after %s; should be a few seconds", d)
	}
}

// A real network failure is not retried as a lock and keeps its message.
func TestFetchOfflineIsNotALock(t *testing.T) {
	origin := world(t, "")
	a := clone(t, origin, "Alice", "", config.Git{Mode: "push"}, nil)
	git(t, a.dir, "remote", "set-url", "origin", origin+".missing")
	start := time.Now()
	err := a.s.fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "offline?") {
		t.Fatalf("want an offline error, got %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("an offline fetch took %s; it should not be retried", d)
	}
}
