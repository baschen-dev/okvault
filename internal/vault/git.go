package vault

import (
	"log/slog"
	"sort"
)

// Committer records changed vault files as a commit. gitsync implements it;
// the vault calls it after each write while holding its write lock.
type Committer interface {
	// Begin is called before a write touches any file. It blocks other git
	// work on the checkout (including other okvault processes) until end runs.
	Begin() (end func(), err error)
	// Commit commits exactly these vault-relative paths (nothing else that
	// happens to be staged). It returns "" when there was nothing to commit.
	Commit(paths []string, subject, body string) (sha string, err error)
}

// Commit reports the commit that recorded a write.
type Commit struct {
	SHA   string `json:"sha,omitempty"`
	Error string `json:"error,omitempty"` // the write is on disk but was not committed
}

// commit hands the files touched by the current write to the Committer.
// A git failure never fails the write: the note is already on disk.
func (v *Vault) commit(subject, body string) *Commit {
	if v.Git == nil || len(v.touched) == 0 {
		return nil
	}
	paths := make([]string, 0, len(v.touched))
	for p := range v.touched {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	sha, err := v.Git.Commit(paths, subject, body)
	if err != nil {
		slog.Warn("git commit failed", "err", err)
		return &Commit{Error: err.Error()}
	}
	if sha == "" {
		return nil
	}
	return &Commit{SHA: sha}
}

// Locked is the vault as seen by a git operation holding the write lock.
type Locked struct{ v *Vault }

// Reload rebuilds the snapshot from the work tree.
func (l Locked) Reload() (*Snapshot, error) { return l.v.Reload() }

// RegenerateIndexes rewrites generated index.md files and repairs log.md.
func (l Locked) RegenerateIndexes() ([]string, error) {
	s, err := l.v.Reload()
	if err != nil {
		return nil, err
	}
	return l.v.regenerateIndexes(s)
}

// Exclusive runs fn with writes blocked and Busy reporting true, for git
// operations that rewrite the work tree (rebase, reset). The file watcher
// checks Busy so it does not regenerate indexes into a tree git is rewriting.
func (v *Vault) Exclusive(fn func(Locked) error) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.busy.Store(true)
	defer v.busy.Store(false)
	return fn(Locked{v})
}

// Busy reports whether a git operation is rewriting the work tree.
func (v *Vault) Busy() bool { return v.busy.Load() }
