// Package gitsync records vault changes in git and keeps the checkout in step
// with the team: it commits okvault's writes, rebases on the remote and
// pushes, either straight to a branch (push mode) or to a personal branch with
// one open GitHub pull request (pr mode). See config.Git for the settings.
//
// It shells out to the git CLI so that the user's credential helpers, SSH
// keys and hooks apply exactly as they do on the command line.
package gitsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// repo runs git in one repository.
type repo struct {
	top    string // repository top-level directory
	prefix string // vault directory inside the repo, "" or "vault/" (slash-terminated)
}

// openRepo finds the repository containing the vault root.
func openRepo(vaultRoot string) (*repo, error) {
	r := &repo{top: vaultRoot}
	out, err := r.run(context.Background(), "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("gitsync: %s is not in a git repository", vaultRoot)
	}
	top := filepath.Clean(strings.TrimSpace(out))
	// compare resolved paths: git prints forward slashes and may resolve symlinks
	absVault, _ := filepath.Abs(vaultRoot)
	if ev, err := filepath.EvalSymlinks(absVault); err == nil {
		absVault = ev
	}
	if et, err := filepath.EvalSymlinks(top); err == nil {
		top = et
	}
	rel, err := filepath.Rel(top, absVault)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("gitsync: cannot place vault %s inside repository %s", vaultRoot, top)
	}
	r.top = top
	if rel = filepath.ToSlash(rel); rel != "." {
		r.prefix = rel + "/"
	}
	return r, nil
}

var credInURL = regexp.MustCompile(`(https?://)[^/@\s]+@`)

// run runs git and returns stdout. Errors carry git's stderr, with any
// credentials in URLs removed.
func (r *repo) run(ctx context.Context, args ...string) (string, error) {
	return r.runEnv(ctx, nil, args...)
}

func (r *repo) runEnv(ctx context.Context, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.top}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0", // never wait for a password on stdin (stdin is MCP)
		"GIT_EDITOR=true",       // rebase --continue keeps messages
		"LC_ALL=C",              // stable messages to match on
	)
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		// fail instead of waiting for a passphrase or host-key prompt nobody sees
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		msg = credInURL.ReplaceAllString(msg, "$1")
		return stdout.String(), fmt.Errorf("git %s: %w: %s", args[0], err, msg)
	}
	return stdout.String(), nil
}

// ok runs a git command that answers yes or no through its exit status.
func (r *repo) ok(ctx context.Context, args ...string) bool {
	_, err := r.run(ctx, args...)
	return err == nil
}

func (r *repo) line(ctx context.Context, args ...string) string {
	out, err := r.run(ctx, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// toRepo maps vault-relative paths to repository paths.
func (r *repo) toRepo(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = r.prefix + p
	}
	return out
}

// vaultSpec is the pathspec covering the whole vault.
func (r *repo) vaultSpec() string {
	if r.prefix == "" {
		return "."
	}
	return strings.TrimSuffix(r.prefix, "/")
}

// statusPaths lists changed and untracked paths matching spec (repo-relative).
func (r *repo) statusPaths(ctx context.Context, spec string) ([]string, error) {
	out, err := r.run(ctx, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--", spec)
	if err != nil {
		return nil, err
	}
	var paths []string
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		code, p := f[:2], f[3:]
		paths = append(paths, p)
		if code[0] == 'R' || code[0] == 'C' {
			i++ // the next field is the rename source
			if i < len(fields) && fields[i] != "" {
				paths = append(paths, fields[i])
			}
		}
	}
	return paths, nil
}

func (r *repo) currentBranch(ctx context.Context) string {
	return r.line(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
}

func (r *repo) rev(ctx context.Context, ref string) string {
	return r.line(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
}

func (r *repo) isAncestor(ctx context.Context, a, b string) bool {
	return r.ok(ctx, "merge-base", "--is-ancestor", a, b)
}

func (r *repo) count(ctx context.Context, rangeSpec string) int {
	n := 0
	fmt.Sscanf(r.line(ctx, "rev-list", "--count", rangeSpec), "%d", &n)
	return n
}

// generated reports whether a repo path is a file okvault rebuilds itself,
// so a conflict in it can be resolved by regenerating.
func (r *repo) generated(p string) bool {
	if !strings.HasPrefix(p, r.prefix) {
		return false
	}
	v := strings.TrimPrefix(p, r.prefix)
	return v == "log.md" || v == "index.md" || strings.HasSuffix(v, "/index.md")
}

// appConfig reports whether a repo path is in the vault's Obsidian config
// folder. Obsidian rewrites those files on its own (opening a vault fills
// core-plugins.json with this install's defaults), so committing them as
// "edits" would make teammates overwrite each other's settings. A team that
// shares settings commits them on purpose, with git.
func (r *repo) appConfig(p string) bool {
	return strings.HasPrefix(p, r.prefix+".obsidian/")
}

// edited filters a list of changed repo paths down to what CommitEdits commits.
func (r *repo) edited(paths []string) []string {
	var out []string
	for _, p := range paths {
		if !r.appConfig(p) {
			out = append(out, p)
		}
	}
	return out
}

var errConflict = errors.New("conflict")
