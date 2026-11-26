package gitsync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/baschen-dev/okvault/internal/config"
	"github.com/baschen-dev/okvault/internal/okf"
	"github.com/baschen-dev/okvault/internal/vault"
)

// Options configures a Syncer.
type Options struct {
	Vault  *vault.Vault
	Config func() config.Git // current settings; read on every operation so config edits apply live
	Mode   string            // overrides Config().Mode when set (`serve --git`)
	Author string            // agent name, used for the default branch when git has no user.name
	Env    func(string) string
	// GitHub overrides the client built from the remote URL (tests).
	GitHub *GitHub
}

// Status is the state of the vault's git sync, as shown to agents and people.
type Status struct {
	Mode     string    `json:"mode"`
	Branch   string    `json:"branch,omitempty"`
	Base     string    `json:"base,omitempty"`     // pr mode: target branch
	Unpushed int       `json:"unpushed,omitempty"` // commits not yet on the remote
	PR       string    `json:"pull_request,omitempty"`
	Problem  string    `json:"problem,omitempty"` // why the last sync did not finish
	LastSync time.Time `json:"last_sync,omitzero"`
}

// Syncer commits vault changes and syncs them with the remote.
type Syncer struct {
	o      Options
	r      *repo
	gmu    sync.Mutex // serialises this process's git work (see lock.go for other processes)
	syncMu sync.Mutex // one Sync at a time
	smu    sync.Mutex
	st     Status
	kick   chan struct{}
	edit   atomic.Int64 // unix nanos of the last change seen by the watcher
}

// New opens the git repository holding the vault.
func New(o Options) (*Syncer, error) {
	if o.Env == nil {
		o.Env = os.Getenv
	}
	r, err := openRepo(o.Vault.Root)
	if err != nil {
		return nil, err
	}
	s := &Syncer{o: o, r: r, kick: make(chan struct{}, 1)}
	s.edit.Store(time.Now().UnixNano())
	return s, nil
}

// Mode is the effective git mode.
func (s *Syncer) Mode() string {
	if s.o.Mode != "" {
		return s.o.Mode
	}
	return s.o.Config().Mode
}

// Status returns the last known sync state.
func (s *Syncer) Status() Status {
	s.smu.Lock()
	defer s.smu.Unlock()
	st := s.st
	st.Mode = s.Mode()
	return st
}

func (s *Syncer) update(f func(*Status)) {
	s.smu.Lock()
	defer s.smu.Unlock()
	f(&s.st)
}

// Kick asks the background loop to sync soon (after a commit).
func (s *Syncer) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Touched records that the watcher saw files change (for commit_edits).
func (s *Syncer) Touched() { s.edit.Store(time.Now().UnixNano()) }

// Commit implements vault.Committer: it commits exactly the given vault
// paths. The vault calls it between Begin and its end function.
func (s *Syncer) Commit(paths []string, subject, body string) (string, error) {
	mode := s.Mode()
	if mode == config.GitOff {
		return "", nil
	}
	ctx := context.Background()
	if mode == config.GitPR {
		if err := s.ensureBranch(ctx); err != nil {
			return "", err
		}
	}
	sha, err := s.commitRepo(ctx, s.r.toRepo(paths), subject, body)
	if sha != "" && mode != config.GitCommit {
		s.Kick()
	}
	return sha, err
}

// commitRepo commits repo paths with --only, so changes the user staged
// elsewhere stay staged and out of okvault's commit. Caller holds gmu.
func (s *Syncer) commitRepo(ctx context.Context, paths []string, subject, body string) (string, error) {
	if len(paths) == 0 {
		return "", nil
	}
	if _, err := s.r.run(ctx, append([]string{"add", "-A", "--"}, paths...)...); err != nil {
		return "", err
	}
	if s.r.ok(ctx, append([]string{"diff", "--cached", "--quiet", "--"}, paths...)...) {
		return "", nil // nothing changed
	}
	args := []string{"commit", "-q", "-m", subject}
	if body != "" {
		args = append(args, "-m", body)
	}
	args = append(append(args, "--only", "--"), paths...)
	if _, err := s.r.run(ctx, args...); err != nil {
		return "", err
	}
	return s.r.rev(ctx, "HEAD"), nil
}

// Run commits edits made outside okvault when they have rested (commit_edits)
// and syncs after okvault's commits and every pull_interval, until ctx ends.
func (s *Syncer) Run(ctx context.Context) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	var debounce <-chan time.Time
	last := time.Time{}
	sync := func() {
		s.Sync(ctx)
		last = time.Now()
	}
	if m := s.Mode(); m == config.GitPush || m == config.GitPR {
		sync()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
			debounce = time.After(2 * time.Second) // one sync for a burst of writes
		case <-debounce:
			debounce = nil
			sync()
		case <-tick.C:
			g := s.o.Config()
			if q := g.Quiet(); q > 0 && time.Since(time.Unix(0, s.edit.Load())) >= q {
				if sha, err := s.CommitEdits(ctx); err != nil {
					slog.Warn("committing edits failed", "err", err)
				} else if sha != "" {
					slog.Info("committed edits made outside okvault", "commit", sha[:min(7, len(sha))])
					s.Kick()
				}
			}
			if e := g.Every(); e > 0 && time.Since(last) >= e {
				sync()
			}
		}
	}
}

// CommitEdits commits every uncommitted change in the vault (edits made in
// Obsidian or by hand), after regenerating indexes so they match.
func (s *Syncer) CommitEdits(ctx context.Context) (string, error) {
	if s.Mode() == config.GitOff {
		return "", nil
	}
	var sha string
	err := s.o.Vault.Exclusive(func(l vault.Locked) error {
		unlock, err := s.lockGit()
		if err != nil {
			return err
		}
		defer unlock()
		if s.Mode() == config.GitPR {
			if err := s.ensureBranch(ctx); err != nil {
				return err
			}
		}
		dirty, err := s.r.statusPaths(ctx, s.r.vaultSpec())
		if dirty = s.r.edited(dirty); err != nil || len(dirty) == 0 {
			return err
		}
		if _, err := l.RegenerateIndexes(); err != nil {
			return err
		}
		if dirty, err = s.r.statusPaths(ctx, s.r.vaultSpec()); err != nil {
			return err
		}
		dirty = s.r.edited(dirty)
		var list []string
		for _, p := range dirty {
			if !s.r.generated(p) {
				list = append(list, "- "+strings.TrimPrefix(p, s.r.prefix))
			}
		}
		subject := "docs(kb): edits made outside okvault"
		if len(list) == 1 {
			subject = "docs(kb): edit " + strings.TrimPrefix(list[0], "- ")
		}
		sha, err = s.commitRepo(ctx, dirty, subject, strings.Join(list, "\n"))
		return err
	})
	return sha, err
}

// Sync fetches, rebases local commits on the remote and pushes them (push
// mode), or keeps the personal branch rebased on the base branch, pushed, and
// proposed in one pull request (pr mode). It returns the resulting status.
//
// Only the rebase holds the vault's write lock: fetching, pushing and the
// GitHub API run without it, so kb_write never waits on the network.
func (s *Syncer) Sync(ctx context.Context) Status {
	mode := s.Mode()
	if mode != config.GitPush && mode != config.GitPR {
		return s.Status()
	}
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	var err error
	if mode == config.GitPush {
		err = s.syncPush(ctx)
	} else {
		err = s.syncPR(ctx)
	}
	s.update(func(st *Status) {
		st.LastSync = time.Now()
		st.Problem = ""
		if err != nil {
			st.Problem = err.Error()
		}
	})
	if err != nil {
		slog.Warn("git sync did not finish", "err", err)
	}
	return s.Status()
}

// locked runs fn holding the vault write lock (no writes, watcher paused) and
// the git lock.
func (s *Syncer) locked(fn func(vault.Locked) error) error {
	return s.o.Vault.Exclusive(func(l vault.Locked) error {
		unlock, err := s.lockGit()
		if err != nil {
			return err
		}
		defer unlock()
		return fn(l)
	})
}

// fetchLockWait is how long a fetch waits for another git process (usually
// another okvault on the same checkout, fetching too) to release a ref.
var fetchLockWait = 3 * time.Second

// fetch runs without the git lock, so kb_write never waits on the network.
// Two okvault processes on one checkout can then fetch at once, and git lets
// only one of them update a remote-tracking ref: the other fails with "cannot
// lock ref". By then the winner has fetched the same refs, so the loser retries
// for a moment instead of reporting the remote as unreachable.
func (s *Syncer) fetch(ctx context.Context) error {
	remote := s.o.Config().Remote
	deadline := time.Now().Add(fetchLockWait)
	pause := 100 * time.Millisecond
	for {
		_, err := s.r.run(ctx, "fetch", "-q", "--prune", remote)
		if err == nil {
			return nil
		}
		if !refLocked(err) {
			return fmt.Errorf("fetch from %s failed (offline?): %w", remote, err)
		}
		if time.Now().Add(pause).After(deadline) {
			return fmt.Errorf("fetch from %s: another git process kept a ref locked; okvault retries on the next sync: %w", remote, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pause):
		}
		pause = min(2*pause, 800*time.Millisecond)
	}
}

// refLocked reports whether git failed because another git process held a
// lock on a ref (or on packed-refs) it had to update. Messages are stable:
// git runs with LC_ALL=C.
func refLocked(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "cannot lock ref") ||
		strings.Contains(msg, ".lock': File exists") ||
		strings.Contains(msg, "some local refs could not be updated")
}

func (s *Syncer) syncPush(ctx context.Context) error {
	remote := s.o.Config().Remote
	if err := s.fetch(ctx); err != nil {
		return err
	}
	var branch, up, head string
	err := s.locked(func(l vault.Locked) error {
		branch = s.r.currentBranch(ctx)
		if branch == "" {
			return errors.New("HEAD is detached; check out a branch so okvault can push")
		}
		up = s.r.line(ctx, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
		if up == "" && s.r.rev(ctx, remote+"/"+branch) != "" {
			up = remote + "/" + branch
		}
		if up != "" {
			if err := s.rebase(ctx, l, up, ""); err != nil {
				return err
			}
		}
		if err := s.regenCommit(ctx, l); err != nil {
			return err
		}
		head = s.r.rev(ctx, "HEAD")
		return nil
	})
	s.update(func(st *Status) { st.Branch, st.Base, st.PR = branch, "", "" })
	if err != nil {
		return err
	}
	if up != "" && s.r.count(ctx, up+".."+head) == 0 {
		s.update(func(st *Status) { st.Unpushed = 0 })
		return nil
	}
	dst := branch
	if up != "" {
		dst = strings.TrimPrefix(up, remote+"/")
	}
	if _, err := s.r.run(ctx, "push", "-q", remote, head+":refs/heads/"+dst); err != nil {
		if up != "" {
			s.update(func(st *Status) { st.Unpushed = s.r.count(ctx, up+"..HEAD") })
		}
		return fmt.Errorf("push failed; okvault retries on the next sync: %w", err)
	}
	if up == "" {
		_, _ = s.r.run(ctx, "branch", "-q", "--set-upstream-to="+remote+"/"+dst, branch)
	}
	s.update(func(st *Status) { st.Unpushed = 0 })
	return nil
}

func (s *Syncer) syncPR(ctx context.Context) error {
	remote := s.o.Config().Remote
	if err := s.fetch(ctx); err != nil {
		return err
	}
	b, base := s.branch(ctx), s.base(ctx)
	target := remote + "/" + base
	s.update(func(st *Status) { st.Branch, st.Base = b, base })
	if s.r.rev(ctx, target) == "" {
		return fmt.Errorf("%s does not exist; set git.base", target)
	}
	gh, ghErr := s.github(ctx)
	var merged *PullRequest
	if gh != nil && gh.Token != "" {
		merged, _ = gh.LatestMerged(ctx, b) // network: before taking the lock
	}
	pushedRef := "refs/okvault/pushed/" + b
	remoteHead := s.r.rev(ctx, remote+"/"+b)
	var head string
	err := s.locked(func(l vault.Locked) error {
		if err := s.ensureBranch(ctx); err != nil {
			return err
		}
		if remoteHead != "" && remoteHead != s.r.rev(ctx, pushedRef) && !s.r.isAncestor(ctx, remoteHead, "HEAD") {
			return fmt.Errorf("%s/%s has commits okvault did not push (a teammate's fix?); okvault will not overwrite them. Merge them into %s by hand, or set git.branch to a new name", remote, b, b)
		}
		// After a squash or rebase merge the branch's commits are not in base, so
		// a plain rebase would replay them: drop what the merged pull request
		// carried. A merge-commit merge needs nothing (its head is in base).
		if m := merged; m != nil && m.Head.SHA != "" && s.r.isAncestor(ctx, m.Head.SHA, "HEAD") && !s.r.isAncestor(ctx, m.Head.SHA, target) {
			if s.r.rev(ctx, "HEAD") == m.Head.SHA {
				if _, err := s.r.run(ctx, "reset", "-q", "--keep", target); err != nil {
					return fmt.Errorf("pull request #%d was merged but %s could not be reset to %s: %w", m.Number, b, target, err)
				}
			} else if err := s.rebase(ctx, l, target, m.Head.SHA); err != nil {
				return err
			}
			s.update(func(st *Status) { st.PR = "" })
		}
		if err := s.rebase(ctx, l, target, ""); err != nil {
			return err
		}
		if err := s.regenCommit(ctx, l); err != nil {
			return err
		}
		head = s.r.rev(ctx, "HEAD")
		return nil
	})
	if err != nil {
		return err
	}
	ahead := s.r.count(ctx, target+".."+head)
	if ahead == 0 {
		s.update(func(st *Status) { st.Unpushed, st.PR = 0, "" })
		return nil
	}
	if head != remoteHead {
		// Our own branch, rebased: replace it, but only if it is still what we saw.
		lease := "--force-with-lease=refs/heads/" + b + ":" + remoteHead
		if _, err := s.r.run(ctx, "push", "-q", lease, remote, head+":refs/heads/"+b); err != nil {
			s.update(func(st *Status) { st.Unpushed = ahead })
			return fmt.Errorf("push to %s/%s failed; okvault retries on the next sync: %w", remote, b, err)
		}
		_, _ = s.r.run(ctx, "update-ref", pushedRef, head)
	}
	s.update(func(st *Status) { st.Unpushed = 0 })
	if gh == nil {
		return fmt.Errorf("pushed %s, but cannot open a pull request: %v", b, ghErr)
	}
	if gh.Token == "" {
		s.update(func(st *Status) { st.PR = gh.CompareURL(base, b) })
		return fmt.Errorf("pushed %s; no GitHub token (GH_TOKEN, or `gh auth login`), so open the pull request at %s", b, gh.CompareURL(base, b))
	}
	pr, err := gh.EnsurePR(ctx, b, base, s.prTitle(ctx), s.prBody(ctx, target+".."+head, b, base))
	if pr != nil {
		s.update(func(st *Status) { st.PR = pr.URL })
	}
	return err
}

func (s *Syncer) prTitle(ctx context.Context) string {
	who := s.r.line(ctx, "config", "user.name")
	if who == "" {
		who = s.o.Author
	}
	return "docs(kb): knowledge updates from " + who
}

func (s *Syncer) prBody(ctx context.Context, revs, b, base string) string {
	log := s.r.line(ctx, "log", "--reverse", "--format=- %s", revs)
	return "Changes recorded by okvault:\n\n" + log + "\n\n" +
		"okvault keeps `" + b + "` rebased on `" + base + "` and force-pushes it, so push fixes to another branch, " +
		"or suggest them in review. Review the notes as you would code; merging publishes them to the team.\n"
}

// regenCommit regenerates indexes (after a rebase) and commits any change.
func (s *Syncer) regenCommit(ctx context.Context, l vault.Locked) error {
	written, err := l.RegenerateIndexes()
	if err != nil || len(written) == 0 {
		return err
	}
	_, err = s.commitRepo(ctx, s.r.toRepo(written), "chore(kb): regenerate indexes", "")
	return err
}

// ensureBranch checks out the pr-mode working branch, creating it from the
// current commit. Uncommitted edits come along.
func (s *Syncer) ensureBranch(ctx context.Context) error {
	b := s.branch(ctx)
	if s.r.currentBranch(ctx) == b {
		return nil
	}
	var err error
	if s.r.rev(ctx, "refs/heads/"+b) == "" {
		_, err = s.r.run(ctx, "switch", "-q", "-c", b)
	} else {
		_, err = s.r.run(ctx, "switch", "-q", b)
	}
	if err != nil {
		return fmt.Errorf("cannot switch to %s for pr mode: %w", b, err)
	}
	slog.Info("git: working on branch", "branch", b)
	return nil
}

// branch is the pr-mode working branch: git.branch, else okvault/<user>.
func (s *Syncer) branch(ctx context.Context) string {
	if b := s.o.Config().Branch; b != "" {
		return b
	}
	who := s.r.line(ctx, "config", "user.name")
	if who == "" {
		who = s.o.Author
	}
	return "okvault/" + slug(who)
}

// base is the branch pull requests target: git.base, else the remote's default.
func (s *Syncer) base(ctx context.Context) string {
	g := s.o.Config()
	if g.Base != "" {
		return g.Base
	}
	if h := s.r.line(ctx, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+g.Remote+"/HEAD"); h != "" {
		return strings.TrimPrefix(h, g.Remote+"/")
	}
	for _, b := range []string{"main", "master"} {
		if s.r.rev(ctx, g.Remote+"/"+b) != "" {
			return b
		}
	}
	return "main"
}

func (s *Syncer) github(ctx context.Context) (*GitHub, error) {
	if s.o.GitHub != nil {
		return s.o.GitHub, nil
	}
	u := s.r.line(ctx, "remote", "get-url", s.o.Config().Remote)
	return newGitHub(u, s.o.Env)
}

// rebase replays local commits onto `onto` (from `upstream` when set, as in
// `git rebase --onto onto upstream`). Conflicts only in generated files
// (index.md, log.md) are resolved by regenerating them; any other conflict
// aborts the rebase, leaving the branch as it was.
func (s *Syncer) rebase(ctx context.Context, l vault.Locked, onto, upstream string) error {
	if upstream == "" && s.r.isAncestor(ctx, onto, "HEAD") {
		return nil // already up to date
	}
	// --autostash would park uncommitted edits and could fail to put them back
	// when incoming commits change the same files: refuse instead.
	if err := s.clearDirty(ctx, onto); err != nil {
		return err
	}
	args := []string{"rebase", "-q", "--autostash"}
	if upstream != "" {
		args = append(args, "--onto", onto, upstream)
	} else {
		args = append(args, onto)
	}
	_, err := s.r.run(ctx, args...)
	for i := 0; err != nil && i < 200; i++ {
		if !s.rebasing(ctx) {
			break
		}
		unmerged := strings.Fields(s.r.line(ctx, "diff", "--name-only", "--diff-filter=U"))
		if len(unmerged) == 0 {
			// the replayed commit became empty (already upstream)
			_, err = s.r.run(ctx, "rebase", "--skip")
			continue
		}
		var hard []string
		for _, p := range unmerged {
			if !s.r.generated(p) {
				hard = append(hard, p)
			}
		}
		if len(hard) > 0 {
			_, _ = s.r.run(ctx, "rebase", "--abort")
			return fmt.Errorf("%w: %s changed both here and on %s; okvault left your commits as they were. Resolve with `git rebase %s` (or edit the note and write it again)", errConflict, strings.Join(hard, ", "), onto, onto)
		}
		for _, p := range unmerged {
			if strings.HasSuffix(p, "log.md") && !strings.HasSuffix(p, "index.md") {
				a, _ := s.r.run(ctx, "show", ":2:"+p)
				b, _ := s.r.run(ctx, "show", ":3:"+p)
				if werr := os.WriteFile(filepath.Join(s.r.top, filepath.FromSlash(p)), okf.MergeLogs([]byte(a), []byte(b)), 0o644); werr != nil {
					_, _ = s.r.run(ctx, "rebase", "--abort")
					return werr
				}
			} else {
				_, _ = s.r.run(ctx, "checkout", "--theirs", "--", p)
			}
		}
		regen, rerr := l.RegenerateIndexes()
		if rerr != nil {
			_, _ = s.r.run(ctx, "rebase", "--abort")
			return rerr
		}
		// stage only what was resolved or regenerated: untracked notes being
		// typed in Obsidian are still in the tree and must stay out
		stage := append(append([]string{}, unmerged...), s.r.toRepo(regen)...)
		if _, aerr := s.r.run(ctx, append([]string{"add", "--"}, stage...)...); aerr != nil {
			_, _ = s.r.run(ctx, "rebase", "--abort")
			return aerr
		}
		_, err = s.r.run(ctx, "rebase", "--continue")
		if err != nil && s.rebasing(ctx) && len(strings.Fields(s.r.line(ctx, "diff", "--name-only", "--diff-filter=U"))) == 0 &&
			s.r.ok(ctx, "diff", "--cached", "--quiet") {
			_, err = s.r.run(ctx, "rebase", "--skip")
		}
	}
	if err != nil {
		if s.rebasing(ctx) {
			_, _ = s.r.run(ctx, "rebase", "--abort")
		}
		return fmt.Errorf("rebase onto %s failed: %w", onto, err)
	}
	return nil
}

func (s *Syncer) rebasing(ctx context.Context) bool {
	for _, d := range []string{"rebase-merge", "rebase-apply"} {
		p := s.r.line(ctx, "rev-parse", "--git-path", d)
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(s.r.top, p)
		}
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return true
		}
	}
	return false
}

// clearDirty makes sure no uncommitted edit is lost when rebasing onto
// `onto`: generated files are discarded (they are rebuilt), edits identical
// to the incoming version are dropped, and any other overlap is a conflict.
func (s *Syncer) clearDirty(ctx context.Context, onto string) error {
	dirty, err := s.r.statusPaths(ctx, ".")
	if err != nil || len(dirty) == 0 {
		return err
	}
	mb := s.r.line(ctx, "merge-base", "HEAD", onto)
	if mb == "" {
		return fmt.Errorf("no common history with %s", onto)
	}
	incoming := map[string]bool{}
	for _, p := range strings.Split(s.r.line(ctx, "diff", "--name-only", mb, onto), "\n") {
		if p != "" {
			incoming[p] = true
		}
	}
	var clash []string
	for _, p := range dirty {
		if !incoming[p] {
			continue
		}
		abs := filepath.Join(s.r.top, filepath.FromSlash(p))
		// hash-object applies the repo's filters (eol, autocrlf) like `git add`
		same := false
		if id := s.r.line(ctx, "hash-object", "--", p); id != "" {
			same = id == s.r.line(ctx, "rev-parse", "--verify", "--quiet", onto+":"+p)
		}
		if !s.r.generated(p) && !same {
			clash = append(clash, p)
			continue
		}
		if s.r.ok(ctx, "cat-file", "-e", "HEAD:"+p) {
			_, _ = s.r.run(ctx, "checkout", "-q", "HEAD", "--", p)
		} else {
			_ = os.Remove(abs)
		}
	}
	if len(clash) > 0 {
		return fmt.Errorf("%w: uncommitted edits to %s, and %s changes them too; commit or undo your edits, then sync again", errConflict, strings.Join(clash, ", "), onto)
	}
	return nil
}

func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if out == "" {
		return "agent"
	}
	return out
}
