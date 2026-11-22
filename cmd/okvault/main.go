// Command okvault serves an Obsidian vault in Open Knowledge Format (OKF) to
// AI agents over MCP, and provides CLI helpers to search, lint and index it.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/baschen-dev/okvault/internal/search"
	"github.com/baschen-dev/okvault/internal/server"
	"github.com/baschen-dev/okvault/internal/vault"
)

var version = "dev" // set with -ldflags "-X main.version=..."

const usage = `okvault - OKF knowledge vault for AI agents (MCP)

Usage:
  okvault serve  [--vault DIR] [--read-only] [--author NAME] [--no-watch]   MCP server on stdio
  okvault ask    [--vault DIR] QUESTION...                                  FAQ fast answer
  okvault search [--vault DIR] [--type T] [--tag T] [--limit N] QUERY...    ranked search
  okvault lint   [--vault DIR] [--strict]                                   OKF conformance check
  okvault index  [--vault DIR]                                              regenerate index.md files
  okvault version

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
	_ = fs.Parse(args)
	v, ix, err := open(fs, *dir)
	if err != nil {
		return err
	}
	srv := server.New(v, ix, server.Options{ReadOnly: *readOnly, Author: *author, Version: version})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if !*noWatch {
		go func() {
			onReload := func(s *vault.Snapshot) {
				srv.Refresh(s)
				if *readOnly {
					return
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
	slog.Info("okvault serving", "vault", v.Root, "concepts", len(v.Snapshot().Concepts), "read_only", *readOnly)
	return srv.MCP.Run(ctx, &mcp.StdioTransport{})
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

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
