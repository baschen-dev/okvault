// Package gaps records the questions kb_ask could not answer confidently, so
// people can see what agents needed that the vault does not say yet.
//
// The log is per machine and lives outside the vault (the user cache
// directory by default), so it is never committed: questions can carry
// details of the work an agent was doing. Every okvault process serving the
// same vault on this machine appends to the same file.
package gaps

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry is one line of the log: a question an agent asked, or a dismissal.
type Entry struct {
	At       time.Time `json:"at"`
	Question string    `json:"q,omitempty"`
	Client   string    `json:"client,omitempty"`  // MCP client name, e.g. claude-code
	Dismiss  string    `json:"dismiss,omitempty"` // a person hid this group (its ID)
}

// Log is an append-only JSON Lines file.
type Log struct {
	path string
	mu   sync.Mutex
}

// MaxQuestion caps a recorded question, in runes.
const MaxQuestion = 500

const (
	maxBytes  = 1 << 20      // rotate past 1 MiB...
	keepBytes = maxBytes / 2 // ...keeping the newest lines that fit in half of it
)

// Open returns the log at path (created on the first write).
func Open(path string) *Log { return &Log{path: path} }

// Path is where the log is written.
func (l *Log) Path() string { return l.path }

// DefaultPath is the per-machine log for a vault: the user cache directory,
// keyed by the vault's absolute path.
func DefaultPath(vaultRoot string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(vaultRoot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.ToSlash(abs))))
	return filepath.Join(dir, "okvault", "gaps-"+hex.EncodeToString(sum[:6])+".jsonl"), nil
}

// Record appends a question.
func (l *Log) Record(question, client string, at time.Time) error {
	q := strings.Join(strings.Fields(question), " ")
	if r := []rune(q); len(r) > MaxQuestion {
		q = string(r[:MaxQuestion])
	}
	if q == "" {
		return nil
	}
	return l.append(Entry{At: at.UTC(), Question: q, Client: client})
}

// Dismiss hides a group from now on.
func (l *Log) Dismiss(id string, at time.Time) error {
	if id == "" {
		return errors.New("gaps: empty id")
	}
	return l.append(Entry{At: at.UTC(), Dismiss: id})
}

func (l *Log) append(e Entry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	// One O_APPEND write per line keeps concurrent okvault processes from interleaving.
	f, err := openRetry(func() (*os.File, error) {
		return os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	})
	if err != nil {
		return err
	}
	_, werr := f.Write(append(b, '\n'))
	st, serr := f.Stat()
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil && serr == nil && st.Size() > maxBytes {
		// Best effort: on Windows the rename fails while another okvault has the
		// file open; the next write past the limit tries again.
		_ = l.rotate()
	}
	return werr
}

// openRetry opens the log, waiting out the moment (on Windows) when another
// okvault is renaming its rotated copy into place.
func openRetry(open func() (*os.File, error)) (*os.File, error) {
	for i := 0; ; i++ {
		f, err := open()
		if err == nil || i == 6 || !busy(err) {
			return f, err
		}
		time.Sleep(time.Duration(5<<i) * time.Millisecond) // 5ms .. 160ms, ~315ms in all
	}
}

// rotate keeps the newest lines. Caller holds mu. A line another process
// appends between the read and the rename is lost; acceptable for this log.
func (l *Log) rotate() error {
	b, err := os.ReadFile(l.path)
	if err != nil {
		return err
	}
	lines := bytes.Split(bytes.TrimRight(b, "\n"), []byte("\n"))
	size, from := 0, len(lines)
	for from > 0 && size+len(lines[from-1])+1 <= keepBytes {
		from--
		size += len(lines[from]) + 1
	}
	lines = lines[from:]
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, append(bytes.Join(lines, []byte("\n")), '\n'), 0o600); err != nil {
		return err
	}
	// Windows refuses the rename while anyone (another okvault, a virus
	// scanner, the indexer) holds the file; such holds are brief.
	for i := 0; ; i++ {
		err = os.Rename(tmp, l.path)
		if err == nil || i == 4 {
			if err != nil {
				_ = os.Remove(tmp)
			}
			return err
		}
		time.Sleep(time.Duration(10<<i) * time.Millisecond)
	}
}

// Read returns the recorded questions (oldest first) and the dismissed group IDs.
// A missing log is empty; unreadable lines are skipped.
func (l *Log) Read() ([]Entry, map[string]bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := openRetry(func() (*os.File, error) { return os.Open(l.path) })
	if errors.Is(err, os.ErrNotExist) {
		return nil, map[string]bool{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	var qs []Entry
	dismissed := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		switch {
		case e.Dismiss != "":
			dismissed[e.Dismiss] = true
		case e.Question != "":
			qs = append(qs, e)
		}
	}
	sort.SliceStable(qs, func(i, j int) bool { return qs[i].At.Before(qs[j].At) })
	return qs, dismissed, sc.Err()
}

// Group is a set of questions asking the same thing in different words.
type Group struct {
	ID        string    // stable: derived from the earliest phrasing
	Phrasings []string  // distinct phrasings, most asked first
	Counts    []int     // how often each phrasing was asked
	Asked     int       // total
	First     time.Time // first asked
	Last      time.Time // last asked
	Clients   []string  // who asked (MCP client names)
}

// Similar reports how alike two questions are (0..1); Normalize gives a
// question's canonical form. search.Similarity and search.Normalize in practice.
type (
	Similar   func(a, b string) float64
	Normalize func(string) string
)

// SameQuestion is the similarity from which two phrasings form one group:
// kb_ask's own FAQ threshold.
const SameQuestion = 0.6

// GroupQuestions groups entries (oldest first) whose phrasings are at least
// SameQuestion alike, in order of first appearance.
func GroupQuestions(entries []Entry, sim Similar, norm Normalize) []*Group {
	type phr struct {
		text  string
		count int
	}
	type acc struct {
		g       *Group
		phrases []*phr
		byNorm  map[string]*phr
		clients map[string]bool
	}
	var groups []*acc
	for _, e := range entries {
		n := norm(e.Question)
		if n == "" {
			continue
		}
		var into *acc
		best := 0.0
		for _, a := range groups {
			if a.byNorm[n] != nil {
				into, best = a, 1
				break
			}
			for _, p := range a.phrases {
				if s := sim(e.Question, p.text); s >= SameQuestion && s > best {
					into, best = a, s
				}
			}
		}
		if into == nil {
			into = &acc{g: &Group{ID: id(n), First: e.At}, byNorm: map[string]*phr{}, clients: map[string]bool{}}
			groups = append(groups, into)
		}
		p := into.byNorm[n]
		if p == nil {
			p = &phr{text: e.Question}
			into.byNorm[n] = p
			into.phrases = append(into.phrases, p)
		}
		p.count++
		into.g.Asked++
		into.g.Last = e.At
		if e.Client != "" {
			into.clients[e.Client] = true
		}
	}
	out := make([]*Group, 0, len(groups))
	for _, a := range groups {
		sort.SliceStable(a.phrases, func(i, j int) bool { return a.phrases[i].count > a.phrases[j].count })
		for _, p := range a.phrases {
			a.g.Phrasings = append(a.g.Phrasings, p.text)
			a.g.Counts = append(a.g.Counts, p.count)
		}
		for c := range a.clients {
			a.g.Clients = append(a.g.Clients, c)
		}
		sort.Strings(a.g.Clients)
		out = append(out, a.g)
	}
	return out
}

func id(norm string) string {
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:5])
}
