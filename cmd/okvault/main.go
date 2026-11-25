// Command okvault serves an Obsidian vault in Open Knowledge Format (OKF) to
// AI agents over MCP, and provides CLI helpers to search, lint and index it.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/baschen-dev/okvault/internal/config"
	"github.com/baschen-dev/okvault/internal/gaps"
	"github.com/baschen-dev/okvault/internal/gitsync"
	"github.com/baschen-dev/okvault/internal/search"
	"github.com/baschen-dev/okvault/internal/server"
	"github.com/baschen-dev/okvault/internal/tracker"
	"github.com/baschen-dev/okvault/internal/vault"
)

var version = "dev" // set with -ldflags "-X main.version=..."

const usage = `okvault - OKF knowledge vault for AI agents (MCP)

Usage:
  okvault serve  [--vault DIR] [--read-only] [--author NAME] [--no-watch] [--git MODE]
                 [--http] [--port N] [--host ADDR] [--gaps FILE|off]
                                                MCP server on stdio, or with --http / --port on
                                                http://HOST:PORT/mcp (default 127.0.0.1:8765)
  okvault ask    [--vault DIR] QUESTION...                                  FAQ fast answer
  okvault search [--vault DIR] [--type T] [--tag T] [--limit N] QUERY...    ranked search
  okvault lint   [--vault DIR] [--strict] [--config FILE|-]                 OKF conformance check
  okvault index  [--vault DIR]                                              regenerate index.md files, repair log.md
  okvault ticket [--vault DIR] KEY                                          ticket + related notes
  okvault review [--vault DIR] [--owner O] [--reason R]                     notes needing a person
  okvault gaps   [--vault DIR] [--limit N] [--json] [--gaps FILE]           questions agents asked that no FAQ answers
  okvault verify [--vault DIR] [--by NAME] PATH...                          mark notes checked by a person
  okvault sync   [--vault DIR] [--git MODE] [--json]                        commit edits, rebase, push / update the PR
  okvault connections [--vault DIR] [--json] [--jira-url URL] [--jira-api 2|3] [--no-push-check]
                                                what this machine's Jira and GitHub credentials reach
  okvault version

Jira credentials (for ticket / kb_ticket): JIRA_API_TOKEN, plus JIRA_EMAIL on Jira Cloud.
Git: set git.mode in .okvault.yaml (off, commit, push, pr); --git overrides it on this machine.
GitHub pull requests (pr mode): GH_TOKEN or GITHUB_TOKEN, else the GitHub CLI's login.

Knowledge gaps: serve records the questions kb_ask cannot answer confidently in a per-machine
file outside the vault (the user cache directory); --gaps off turns that off.

The vault defaults to $OKVAULT_DIR, else ./vault if it exists, else the current directory.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	// stdout belongs to the MCP protocol in serve mode: log to stderr only.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel()})))
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "serve":
		err = serve(args)
	case "ask":
		err = ask(args, os.Stdout)
	case "search":
		err = searchCmd(args, os.Stdout)
	case "lint":
		err = lint(args, os.Stdin, os.Stdout)
	case "index":
		err = index(args, os.Stdout)
	case "ticket":
		err = ticketCmd(args, os.Stdout)
	case "review":
		err = review(args, os.Stdout)
	case "gaps":
		err = gapsCmd(args, os.Stdout)
	case "connections":
		err = connectionsCmd(args, os.Stdout)
	case "verify":
		err = verify(args, os.Stdout)
	case "sync":
		err = syncCmd(args, os.Stdout)
	case "version", "--version", "-v":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "okvault:", err)
		os.Exit(1)
	}
}

func logLevel() slog.Level {
	if os.Getenv("OKVAULT_DEBUG") != "" {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

func defaultVault() string {
	if d := os.Getenv("OKVAULT_DIR"); d != "" {
		return d
	}
	if st, err := os.Stat("vault"); err == nil && st.IsDir() {
		return "vault"
	}
	return "."
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(s string) error { *m = append(*m, s); return nil }

func open(fs *flag.FlagSet, dir string) (*vault.Vault, *search.Index, error) {
	v, err := vault.Open(dir)
	if err != nil {
		return nil, nil, err
	}
	ix, err := search.Build(v.Snapshot())
	return v, ix, err
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dir := fs.String("vault", defaultVault(), "vault directory")
	readOnly := fs.Bool("read-only", false, "do not expose kb_write")
	author := fs.String("author", envOr("OKVAULT_AUTHOR", "agent"), "recorded as generated.by on writes")
	noWatch := fs.Bool("no-watch", false, "do not reload when files change")
	gitMode := fs.String("git", "", "override git.mode from .okvault.yaml on this machine: off, commit, push or pr")
	useHTTP := fs.Bool("http", false, "serve MCP over HTTP (Streamable HTTP at /mcp) instead of stdio")
	port := fs.Int("port", 0, fmt.Sprintf("HTTP port; implies --http (default %d)", defaultPort))
	host := fs.String("host", "127.0.0.1", "HTTP listen address; use 0.0.0.0 to accept other machines (no authentication)")
	gapsFile := fs.String("gaps", "", "where to record questions kb_ask cannot answer (default: a per-machine file in the user cache directory); off to disable")
	_ = fs.Parse(args)
	if *port < 0 || *port > 65535 {
		return fmt.Errorf("--port %d is out of range", *port)
	}
	v, ix, err := open(fs, *dir)
	if err != nil {
		return err
	}
	opts := server.Options{ReadOnly: *readOnly, Author: *author, Version: version, Env: os.Getenv}
	if opts.Gaps, err = openGaps(*gapsFile, v.Root); err != nil {
		slog.Warn("knowledge gaps are not recorded", "err", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var gs *gitsync.Syncer
	if !*readOnly {
		if gs, err = openGit(v, *gitMode, *author); err != nil {
			return err
		}
	}
	if gs != nil {
		v.Git = gs
		opts.GitStatus = func() any {
			if gs.Mode() == config.GitOff {
				return nil
			}
			return gs.Status()
		}
		go gs.Run(ctx)
	}
	srv := server.New(v, ix, opts)
	if !*noWatch {
		go func() {
			onReload := func(s *vault.Snapshot) {
				srv.Refresh(s)
				if *readOnly || v.Busy() || (gs != nil && gs.InProgress()) {
					return // git is rewriting the tree (here or in another process); indexes are regenerated after it
				}
				if gs != nil {
					gs.Touched()
				}
				// keep okvault-owned index.md files in step with notes edited in Obsidian
				if written, err := v.RegenerateIndexes(); err != nil {
					slog.Warn("index regeneration failed", "err", err)
				} else if len(written) > 0 {
					slog.Debug("indexes regenerated", "files", written)
				}
			}
			if err := v.Watch(ctx, 300*time.Millisecond, onReload); err != nil {
				slog.Warn("watcher stopped", "err", err)
			}
		}()
	}
	if *useHTTP || *port != 0 {
		if *port == 0 {
			*port = defaultPort
		}
		return serveHTTP(ctx, srv.MCP, net.JoinHostPort(*host, strconv.Itoa(*port)), v.Root, *readOnly)
	}
	slog.Info("okvault serving", "transport", "stdio", "vault", v.Root, "concepts", len(v.Snapshot().Concepts), "read_only", *readOnly)
	return srv.MCP.Run(ctx, &mcp.StdioTransport{})
}

// defaultPort is the --http port when --port is not given.
const defaultPort = 8765

// serveHTTP serves the MCP server over Streamable HTTP at /mcp on addr until
// ctx is cancelled.
func serveHTTP(ctx context.Context, s *mcp.Server, addr, root string, readOnly bool) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return serveListener(ctx, s, ln, root, readOnly)
}

func serveListener(ctx context.Context, s *mcp.Server, ln net.Listener, root string, readOnly bool) error {
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil))
	hs := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(shut)
	}()
	slog.Info("okvault serving", "transport", "http", "url", "http://"+ln.Addr().String()+"/mcp", "vault", root, "read_only", readOnly)
	if err := hs.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// openGit returns the vault's git syncer, or nil when git is off and the
// vault is not in a repository. mode overrides git.mode from .okvault.yaml.
func openGit(v *vault.Vault, mode, author string) (*gitsync.Syncer, error) {
	switch mode = strings.ToLower(strings.TrimSpace(mode)); mode {
	case "", config.GitOff, config.GitCommit, config.GitPush, config.GitPR:
	default:
		return nil, fmt.Errorf("--git %q must be off, commit, push or pr", mode)
	}
	if mode == config.GitOff {
		return nil, nil
	}
	cfg := func() config.Git { return v.Snapshot().Config.Git }
	s, err := gitsync.New(gitsync.Options{Vault: v, Config: cfg, Mode: mode, Author: author, Env: os.Getenv})
	if err != nil {
		if m := mode; m != "" || cfg().Mode != config.GitOff {
			slog.Warn("git is configured but unavailable", "err", err)
		}
		return nil, nil
	}
	if m := s.Mode(); m != config.GitOff {
		slog.Info("git sync on", "mode", m)
	}
	return s, nil
}

func syncCmd(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	dir := fs.String("vault", defaultVault(), "vault directory")
	asJSON := fs.Bool("json", false, "print JSON")
	gitMode := fs.String("git", "", "override git.mode from .okvault.yaml: commit, push or pr")
	_ = fs.Parse(args)
	v, err := vault.Open(*dir)
	if err != nil {
		return err
	}
	gs, err := openGit(v, *gitMode, envOr("OKVAULT_AUTHOR", "agent"))
	if err != nil {
		return err
	}
	if gs == nil || gs.Mode() == config.GitOff {
		return fmt.Errorf("git is off: set git.mode in %s", config.File)
	}
	ctx := context.Background()
	if sha, err := gs.CommitEdits(ctx); err != nil {
		return err
	} else if sha != "" && !*asJSON {
		fmt.Fprintf(w, "committed edits: %s\n", sha[:min(7, len(sha))])
	}
	st := gs.Sync(ctx)
	if *asJSON {
		return printJSON(w, st)
	}
	fmt.Fprintf(w, "mode %s", st.Mode)
	if st.Branch != "" {
		fmt.Fprintf(w, ", branch %s", st.Branch)
	}
	if st.Base != "" {
		fmt.Fprintf(w, " -> %s", st.Base)
	}
	fmt.Fprintln(w)
	if st.PR != "" {
		fmt.Fprintln(w, "pull request:", st.PR)
	}
	if st.Problem != "" {
		return errors.New(st.Problem)
	}
	return nil
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func ask(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("ask", flag.ExitOnError)
	dir := fs.String("vault", defaultVault(), "vault directory")
	asJSON := fs.Bool("json", false, "print JSON")
	_ = fs.Parse(args)
	_, ix, err := open(fs, *dir)
	if err != nil {
		return err
	}
	a, err := ix.Ask(strings.Join(fs.Args(), " "))
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(w, a)
	}
	if a.Confident {
		fmt.Fprintf(w, "%s  (%.2f, %s)\n\n%s\n", a.Question, a.Confidence, a.Path, a.Answer)
		return nil
	}
	fmt.Fprintf(w, "No confident answer (best %.2f). Related:\n", a.Confidence)
	printHits(w, a.Related)
	return nil
}

func searchCmd(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	dir := fs.String("vault", defaultVault(), "vault directory")
	typ := fs.String("type", "", "filter by type")
	var tags multi
	fs.Var(&tags, "tag", "filter by tag (repeatable)")
	limit := fs.Int("limit", 8, "max hits")
	asJSON := fs.Bool("json", false, "print JSON")
	_ = fs.Parse(args)
	_, ix, err := open(fs, *dir)
	if err != nil {
		return err
	}
	start := time.Now()
	hits, err := ix.Search(search.Query{Text: strings.Join(fs.Args(), " "), Type: *typ, Tags: tags, Limit: *limit})
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(w, hits)
	}
	printHits(w, hits)
	fmt.Fprintf(w, "(%d hits in %s)\n", len(hits), time.Since(start).Round(time.Microsecond))
	return nil
}

func printHits(w io.Writer, hits []search.Hit) {
	for _, h := range hits {
		fmt.Fprintf(w, "%6.2f  %-40s [%s] %s\n", h.Score, h.Path, h.Type, h.Title)
		if h.Snippet != "" {
			fmt.Fprintf(w, "        %s\n", h.Snippet)
		}
	}
}

func lint(args []string, stdin io.Reader, w io.Writer) error {
	fs := flag.NewFlagSet("lint", flag.ExitOnError)
	dir := fs.String("vault", defaultVault(), "vault directory")
	strict := fs.Bool("strict", false, "fail on warnings too")
	cfgFile := fs.String("config", "", "check only this "+config.File+" (- reads it from stdin), not a vault")
	_ = fs.Parse(args)
	if *cfgFile != "" {
		return lintConfig(*cfgFile, stdin, w)
	}
	v, err := vault.Open(*dir)
	if err != nil {
		return err
	}
	findings := v.Lint()
	errs, warns := 0, 0
	for _, f := range findings {
		fmt.Fprintf(w, "%-7s %s: %s\n", f.Severity, f.Path, f.Message)
		if f.Severity == vault.Error {
			errs++
		} else {
			warns++
		}
	}
	fmt.Fprintf(w, "%d notes, %d errors, %d warnings\n", len(v.Snapshot().Concepts), errs, warns)
	if errs > 0 || (*strict && warns > 0) {
		return fmt.Errorf("lint failed")
	}
	return nil
}

// lintConfig checks a draft .okvault.yaml the way lint checks the vault's own,
// with the same output, so an editor can validate it before writing it.
func lintConfig(path string, stdin io.Reader, w io.Writer) error {
	var src []byte
	var err error
	if path == "-" {
		src, err = io.ReadAll(stdin)
	} else {
		src, err = os.ReadFile(path)
	}
	if err != nil {
		return err
	}
	if _, err := config.Parse(src); err != nil {
		fmt.Fprintf(w, "%-7s %s: %s\n", vault.Error, config.File, err)
		fmt.Fprintln(w, "1 errors, 0 warnings")
		return fmt.Errorf("lint failed")
	}
	fmt.Fprintln(w, "0 errors, 0 warnings")
	return nil
}

func index(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	dir := fs.String("vault", defaultVault(), "vault directory")
	_ = fs.Parse(args)
	v, err := vault.Open(*dir)
	if err != nil {
		return err
	}
	written, err := v.RegenerateIndexes()
	for _, p := range written {
		fmt.Fprintln(w, "wrote", p)
	}
	if err == nil && len(written) == 0 {
		fmt.Fprintln(w, "indexes up to date")
	}
	return err
}

func ticketCmd(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("ticket", flag.ExitOnError)
	dir := fs.String("vault", defaultVault(), "vault directory")
	asJSON := fs.Bool("json", false, "print JSON")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: okvault ticket KEY")
	}
	v, ix, err := open(fs, *dir)
	if err != nil {
		return err
	}
	srv := server.New(v, ix, server.Options{ReadOnly: true, Version: version, Env: os.Getenv})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := srv.TicketContext(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(w, out)
	}
	fmt.Fprintf(w, "%s  %s\n", out.Key, out.URL)
	if t := out.Ticket; t != nil {
		fmt.Fprintf(w, "%s\n[%s] %s, assignee %s, updated %s\n", t.Summary, t.Type, t.Status, orDash(t.Assignee), orDash(t.Updated))
	} else if out.TrackerNote != "" {
		fmt.Fprintf(w, "(ticket not fetched: %s)\n", out.TrackerNote)
	}
	section := func(name string, es []server.Entry) {
		if len(es) == 0 {
			return
		}
		fmt.Fprintf(w, "\n%s:\n", name)
		for _, e := range es {
			fmt.Fprintf(w, "  %-40s [%s] %s\n", e.Path, e.Type, e.Title)
		}
	}
	section("Linked notes", out.Linked)
	section("Mentioned in", out.Mentioned)
	if len(out.Suggested) > 0 {
		fmt.Fprintln(w, "\nPossibly relevant:")
		printHits(w, out.Suggested)
	}
	fmt.Fprintf(w, "\n%s\n", out.Hint)
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Connections is what `okvault connections` reports.
type Connections struct {
	Jira    *tracker.Check      `json:"jira,omitempty"` // nil: no tracker URL configured or given
	GitMode string              `json:"git_mode"`
	GitHub  gitsync.GitHubCheck `json:"github"`
}

// connectionsCmd checks the credentials this machine gives okvault (Jira from
// JIRA_API_TOKEN/JIRA_EMAIL; GitHub from GH_TOKEN, GITHUB_TOKEN or the GitHub
// CLI; git from its credential helper or SSH agent) and lists Jira projects.
// It reports where credentials come from, never their values.
func connectionsCmd(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("connections", flag.ExitOnError)
	dir := fs.String("vault", defaultVault(), "vault directory")
	asJSON := fs.Bool("json", false, "print JSON")
	jiraURL := fs.String("jira-url", "", "check this Jira site instead of tracker.url from "+config.File)
	jiraAPI := fs.String("jira-api", "", "Jira REST API version, 2 or 3 (default: as okvault picks it)")
	noPush := fs.Bool("no-push-check", false, "skip the git push --dry-run")
	_ = fs.Parse(args)
	cfg, err := config.Load(*dir)
	if err != nil {
		return err
	}
	ctx := context.Background()
	out := Connections{GitMode: cfg.Git.Mode}
	u, api := strings.TrimSpace(*jiraURL), strings.TrimSpace(*jiraAPI)
	if u == "" {
		u = cfg.Tracker.URL
		if api == "" {
			api = cfg.Tracker.API
		}
	}
	if u != "" {
		c := tracker.CheckJira(ctx, u, api, os.Getenv, nil)
		out.Jira = &c
	}
	out.GitHub = gitsync.CheckGitHub(ctx, *dir, gitsync.CheckOptions{Remote: cfg.Git.Remote, Env: os.Getenv, PushTest: !*noPush})
	if *asJSON {
		return printJSON(w, out)
	}
	if j := out.Jira; j == nil {
		fmt.Fprintf(w, "Jira:   no tracker.url in %s (or pass --jira-url)\n", config.File)
	} else {
		fmt.Fprintf(w, "Jira:   %s (REST %s), credentials: %s\n", j.URL, j.API, j.Credentials)
		if j.User != "" {
			fmt.Fprintf(w, "        signed in as %s; %d projects\n", j.User, len(j.Projects))
			for _, p := range j.Projects {
				fmt.Fprintf(w, "          %-12s %s\n", p.Key, p.Name)
			}
		}
		if j.Problem != "" {
			fmt.Fprintf(w, "        problem: %s\n", j.Problem)
		}
	}
	g := out.GitHub
	fmt.Fprintf(w, "Git:    mode %s, remote %s %s\n", out.GitMode, g.RemoteName, g.Remote)
	if g.Problem != "" {
		fmt.Fprintf(w, "        problem: %s\n", g.Problem)
	}
	if g.Push != "" {
		fmt.Fprintf(w, "        push: %s\n", g.Push)
	}
	if g.Repo != "" {
		fmt.Fprintf(w, "GitHub: %s, token: %s", g.Repo, g.Token)
		if g.User != "" {
			fmt.Fprintf(w, ", signed in as %s", g.User)
		}
		fmt.Fprintln(w)
	}
	if g.APIProblem != "" {
		fmt.Fprintf(w, "        %s\n", g.APIProblem)
	}
	return nil
}

// openGaps opens the knowledge-gaps log: file, or the per-machine default for
// the vault; nil for "off".
func openGaps(file, vaultRoot string) (*gaps.Log, error) {
	switch strings.ToLower(strings.TrimSpace(file)) {
	case "off", "none", "false":
		return nil, nil
	case "":
		p, err := gaps.DefaultPath(vaultRoot)
		if err != nil {
			return nil, err
		}
		return gaps.Open(p), nil
	}
	return gaps.Open(file), nil
}

func gapsCmd(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("gaps", flag.ExitOnError)
	dir := fs.String("vault", defaultVault(), "vault directory")
	limit := fs.Int("limit", 20, "max groups")
	asJSON := fs.Bool("json", false, "print JSON")
	file := fs.String("gaps", "", "the gaps log (default: this machine's log for the vault)")
	_ = fs.Parse(args)
	v, ix, err := open(fs, *dir)
	if err != nil {
		return err
	}
	l, err := openGaps(*file, v.Root)
	if err != nil || l == nil {
		return fmt.Errorf("no gaps log: %v", err)
	}
	out, err := server.BuildGaps(ix, l, *limit)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(w, out)
	}
	for _, g := range out.Gaps {
		fmt.Fprintf(w, "%3dx  %s\n", g.Asked, g.Question)
		for _, p := range g.Phrasings[1:] {
			fmt.Fprintf(w, "      also: %s\n", p)
		}
		if g.Nearest != nil {
			fmt.Fprintf(w, "      nearest FAQ: %s (%.2f, %s)\n", g.Nearest.Question, g.Nearest.Confidence, g.Nearest.Path)
		}
	}
	fmt.Fprintf(w, "%d open, %d answered since, %d dismissed  (log: %s)\n", out.Total, out.Answered, out.Dismissed, out.Log)
	return nil
}

func review(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("review", flag.ExitOnError)
	dir := fs.String("vault", defaultVault(), "vault directory")
	owner := fs.String("owner", "", "only notes owned by this person or group")
	reason := fs.String("reason", "", "only: stale, unverified, draft, broken-links, no-owner")
	asJSON := fs.Bool("json", false, "print JSON")
	_ = fs.Parse(args)
	v, err := vault.Open(*dir)
	if err != nil {
		return err
	}
	items := v.Snapshot().Review(vault.ReviewQuery{Owner: *owner, Reason: *reason}, time.Now())
	if *asJSON {
		return printJSON(w, items)
	}
	for _, it := range items {
		fmt.Fprintf(w, "%-40s %-28s %s\n", it.Path, strings.Join(it.Reasons, ","), strings.Join(it.Owners, ","))
		if it.Detail != "" {
			fmt.Fprintf(w, "        %s\n", it.Detail)
		}
	}
	fmt.Fprintf(w, "%d notes need review\n", len(items))
	return nil
}

func verify(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	dir := fs.String("vault", defaultVault(), "vault directory")
	by := fs.String("by", "", "who checked the notes (default: git config user.name)")
	_ = fs.Parse(args)
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: okvault verify [--by NAME] PATH...")
	}
	name := strings.TrimSpace(*by)
	if name == "" {
		name = gitUserName(*dir)
	}
	if name == "" {
		return fmt.Errorf("no name: pass --by or set git config user.name")
	}
	v, err := vault.Open(*dir)
	if err != nil {
		return err
	}
	gs, err := openGit(v, "", name)
	if err != nil {
		return err
	}
	if gs != nil {
		v.Git = gs
		defer func() {
			if st := gs.Sync(context.Background()); st.Problem != "" {
				fmt.Fprintln(w, "git:", st.Problem)
			} else if st.PR != "" {
				fmt.Fprintln(w, "pull request:", st.PR)
			}
		}()
	}
	for _, p := range fs.Args() {
		if abs, err := filepath.Abs(p); err == nil {
			if rel, err := filepath.Rel(v.Root, abs); err == nil && !strings.HasPrefix(rel, "..") {
				p = rel // a path from the current directory, e.g. vault/faq/x.md
			}
		}
		res, err := v.Verify(p, name, time.Now())
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "verified %s by %s\n", res.Path, name)
	}
	if _, err := v.RegenerateIndexes(); err != nil {
		return err
	}
	return nil
}

// gitUserName returns the git user name of the vault's repository, if any.
// The name, not the email, is recorded: it ends up in a shared repository.
func gitUserName(dir string) string {
	out, err := exec.Command("git", "-C", dir, "config", "user.name").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
