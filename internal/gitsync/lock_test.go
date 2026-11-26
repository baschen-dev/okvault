package gitsync

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/baschen-dev/okvault/internal/config"
)

// On Windows another process deleting okvault.lock makes creating it fail
// with "Access is denied" for a moment: that is a lock being released.
func TestLockWaitsOutPendingDelete(t *testing.T) {
	a := clone(t, world(t, ""), "Alice", "", config.Git{Mode: "push"}, nil)
	real := openLock
	defer func() { openLock = real }()
	denied := 3
	openLock = func(p string) (*os.File, error) {
		if denied > 0 {
			denied--
			return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrPermission}
		}
		return real(p)
	}
	unlock, err := a.s.lockGit()
	if err != nil {
		t.Fatalf("lock while the previous one was being deleted: %v", err)
	}
	unlock()
	if denied != 0 {
		t.Fatalf("lock taken without retrying (%d denials left)", denied)
	}
}

// A .git okvault cannot write to fails soon, with the real error.
func TestLockReportsRealPermissionError(t *testing.T) {
	a := clone(t, world(t, ""), "Alice", "", config.Git{Mode: "push"}, nil)
	real := openLock
	defer func() { openLock = real }()
	openLock = func(p string) (*os.File, error) {
		return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrPermission}
	}
	start := time.Now()
	unlock, err := a.s.lockGit()
	if err == nil {
		unlock()
		t.Fatal("lock taken although the lock file cannot be created")
	}
	if !errors.Is(err, fs.ErrPermission) || strings.Contains(err.Error(), "another okvault") {
		t.Errorf("want the permission error, got %v", err)
	}
	if d := time.Since(start); d > lockDenied+2*time.Second {
		t.Errorf("gave up after %s, want about %s", d, lockDenied)
	}
	// the process's own mutex is released, so the next try is not stuck
	openLock = real
	unlock, err = a.s.lockGit()
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}
