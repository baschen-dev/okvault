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
	"github.com/baschen-dev/okvault/internal/gitsync"
	"github.com/baschen-dev/okvault/internal/search"
	"github.com/baschen-dev/okvault/internal/server"
	"github.com/baschen-dev/okvault/internal/vault"
)

var version = "dev" // set with -ldflags "-X main.version=..."

const usage = `okvault - OKF knowledge vault for AI agents (MCP)

Usage:
  okvault serve  [--vault DIR] [--read-only] [--author NAME] [--no-watch] [--git MODE]
                 [--http] [--port N] [--host ADDR]
                                                MCP server on stdio, or with --http / --port on
                                                http://HOST:PORT/mcp (default 127.0.0.1:8765)
  okvault ask    [--vault DIR] QUESTION...                                  FAQ fast answer
  okvault search [--vault DIR] [--type T] [--tag T] [--limit N] QUERY...    ranked search
  okvault lint   [--vault DIR] [--strict]                                   OKF conformance check
  okvault index  [--vault DIR]                                              regenerate index.md files, repair log.md
  okvault ticket [--vault DIR] KEY                                          ticket + related notes
  okvault review [--vault DIR] [--owner O] [--reason R]                     notes needing a person
  okvault verify [--vault DIR] [--by NAME] PATH...                          mark notes checked by a person
  okvault sync   [--vault DIR] [--git MODE] [--json]                        commit edits, rebase, push / update the PR
  okvault version

Jira credentials (for ticket / kb_ticket): JIRA_API_TOKEN, plus JIRA_EMAIL on Jira Cloud.
Git: set git.mode in .okvault.yaml (off, commit, push, pr); --git overrides it on this machine.
GitHub pull requests (pr mode): GH_TOKEN or GITHUB_TOKEN, else the GitHub CLI's login.

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
		err = lint(args, os.Stdout)
	case "index":
		err = index(args, os.Stdout)
	case "ticket":
		err = ticketCmd(args, os.Stdout)
	case "review":
		err = review(args, os.Stdout)
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
	_ = fs.Parse(args)
	if *port < 0 || *port > 65535 {
		return fmt.Errorf("--port %d is out of range", *port)
	}
	v, ix, err := open(fs, *dir)
	if err != nil {
		return err
	}
	opts := server.Options{ReadOnly: *readOnly, Author: *author, Version: version, Env: os.Getenv}
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

func lint(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("lint", flag.ExitOnError)
	dir := fs.String("vault", defaultVault(), "vault directory")
	strict := fs.Bool("strict", false, "fail on warnings too")
	_ = fs.Parse(args)
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
