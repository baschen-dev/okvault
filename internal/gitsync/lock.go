package gitsync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Every Claude Code session starts its own `okvault serve`, so several
// processes can share one checkout. Each process serialises its own git work
// with gmu; a lock file in the git directory serialises them with each other.

const (
	lockWait  = 45 * time.Second // longest a write waits for another okvault
	lockStale = 3 * time.Minute  // a lock this old was left by a crashed process
	// On Windows, creating a file another process is deleting fails with "Access
	// is denied" until the delete completes: the lock is being released, so wait.
	// A permission error that outlasts this is real (a read-only .git).
	lockDenied = 2 * time.Second
)

// openLock creates the lock file, failing if it exists (a variable for tests).
var openLock = func(p string) (*os.File, error) {
	return os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
}

// gitPath resolves a path inside the repository's git directory.
func (r *repo) gitPath(name string) string {
	p := r.line(context.Background(), "rev-parse", "--git-path", name)
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(r.top, p)
	}
	return p
}

// lockGit takes this process's git mutex and the shared lock file.
func (s *Syncer) lockGit() (func(), error) {
	s.gmu.Lock()
	p := s.r.gitPath("okvault.lock")
	if p == "" {
		s.gmu.Unlock()
		return nil, errors.New("gitsync: cannot find the git directory")
	}
	deadline := time.Now().Add(lockWait)
	var deniedSince time.Time
	for wait := 20 * time.Millisecond; ; wait = min(wait*2, 500*time.Millisecond) {
		f, err := openLock(p)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return func() {
				_ = os.Remove(p)
				s.gmu.Unlock()
			}, nil
		}
		if errors.Is(err, os.ErrPermission) {
			if deniedSince.IsZero() {
				deniedSince = time.Now()
			}
			if time.Since(deniedSince) > lockDenied {
				s.gmu.Unlock()
				return nil, fmt.Errorf("gitsync: lock %s: %w", p, err)
			}
			time.Sleep(min(wait, 50*time.Millisecond))
			continue
		}
		deniedSince = time.Time{}
		if !errors.Is(err, os.ErrExist) {
			s.gmu.Unlock()
			return nil, fmt.Errorf("gitsync: lock %s: %w", p, err)
		}
		if st, serr := os.Stat(p); serr == nil && time.Since(st.ModTime()) > lockStale {
			_ = os.Remove(p) // left behind by a process that died
			continue
		}
		if time.Now().After(deadline) {
			s.gmu.Unlock()
			owner, _ := os.ReadFile(p)
			pid, _ := strconv.Atoi(string(trimNL(owner)))
			return nil, fmt.Errorf("gitsync: another okvault (pid %d) has been using the repository for %s; try again", pid, lockWait)
		}
		time.Sleep(wait)
	}
}

func trimNL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// Begin implements vault.Committer: the vault calls it before writing files,
// so no other okvault process is rebasing the tree it writes into.
func (s *Syncer) Begin() (func(), error) {
	if s.Mode() == "off" {
		return func() {}, nil
	}
	return s.lockGit()
}

// InProgress reports whether git is in the middle of a rebase, merge or
// cherry-pick in this checkout, by any process (okvault or a person).
// Index regeneration must wait until it is over.
func (s *Syncer) InProgress() bool {
	if s.rebasing(context.Background()) {
		return true
	}
	for _, f := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD"} {
		if p := s.r.gitPath(f); p != "" {
			if _, err := os.Stat(p); err == nil {
				return true
			}
		}
	}
	return false
}
