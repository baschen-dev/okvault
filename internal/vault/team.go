package vault

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/baschen-dev/okvault/internal/okf"
)

// Verify records that a person checked a note: it appends `{by, at}` to the
// OKF `verified` list and moves `stale_after` one review interval ahead.
// It does not touch `generated`, so the note reads as human-checked.
func (v *Vault) Verify(p, by string, now time.Time) (*WriteResult, error) {
	rel, err := CleanPath(p)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(by) == "" {
		return nil, fmt.Errorf("vault: verify needs a name (--by)")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.Git != nil {
		end, err := v.Git.Begin()
		if err != nil {
			return nil, err
		}
		defer end()
	}
	v.touched = map[string]bool{}
	defer func() { v.touched = nil }()
	src, err := os.ReadFile(v.abs(rel))
	if err != nil {
		return nil, fmt.Errorf("vault: %s: %w", rel, err)
	}
	c, err := okf.Parse(rel, src)
	if err != nil {
		return nil, fmt.Errorf("vault: cannot verify %s: %w", rel, err)
	}
	at := now.UTC().Truncate(time.Second) // stamps in UTC; the log uses the caller's local date like Write
	if err := c.AddVerified(by, at); err != nil {
		return nil, err
	}
	cfg := v.Snapshot().Config
	if err := c.Set("stale_after", at.Add(cfg.Review()).Format(time.RFC3339)); err != nil {
		return nil, err
	}
	data, err := c.Marshal()
	if err != nil {
		return nil, err
	}
	if err := v.writeFile(rel, data); err != nil {
		return nil, err
	}
	msg := fmt.Sprintf("[%s](%s) verified by %s", c.Title, okf.RelLink(okf.LogFile, rel), by)
	logSrc, _ := v.ReadFile(okf.LogFile)
	if err := v.writeFile(okf.LogFile, okf.AppendLog(logSrc, now, "Verification", msg)); err != nil {
		return nil, err
	}
	snap, err := v.Reload()
	if err != nil {
		return nil, err
	}
	if _, err := v.regenerateIndexes(snap); err != nil {
		return nil, err
	}
	res := &WriteResult{Path: rel, Revision: Revision(data)}
	res.Git = v.commit("docs(kb): verify "+rel, "Checked by "+by+".")
	return res, nil
}

// Review reasons.
const (
	ReasonStale      = "stale"      // stale_after has passed
	ReasonUnverified = "unverified" // written by an agent and not checked by a person since
	ReasonDraft      = "draft"
	ReasonBroken     = "broken-links"
	ReasonNoOwner    = "no-owner" // only reported when the vault configures owners
)

// ReviewItem is a note that needs a person's attention.
type ReviewItem struct {
	Path      string     `json:"path"`
	Title     string     `json:"title"`
	Type      string     `json:"type"`
	Owners    []string   `json:"owners,omitempty"`
	Reasons   []string   `json:"reasons"`
	Generated *okf.Stamp `json:"generated,omitempty"`
	Verified  *okf.Stamp `json:"last_verified,omitempty"`
	Detail    string     `json:"detail,omitempty"`
}

// ReviewQuery filters the review queue.
type ReviewQuery struct {
	Owner  string // only notes owned by this person or group (case-insensitive)
	Reason string // only notes with this reason
}

// Review lists notes needing attention, most reasons first.
func (s *Snapshot) Review(q ReviewQuery, now time.Time) []ReviewItem {
	ownersConfigured := len(s.Config.Owners) > 0
	var out []ReviewItem
	for _, p := range s.Sorted() {
		c := s.Concepts[p]
		if strings.EqualFold(c.Status, "deprecated") {
			continue
		}
		it := ReviewItem{Path: p, Title: c.Title, Type: c.Type, Owners: s.Owners(p)}
		var detail []string
		if t, present, ok := c.StaleAfter(); present && ok && now.After(t) {
			it.Reasons = append(it.Reasons, ReasonStale)
			detail = append(detail, "stale since "+t.Format("2006-01-02"))
		}
		lv, verified := c.LastVerified()
		if verified {
			it.Verified = &lv
		}
		if g, ok := c.Generated(); ok {
			it.Generated = &g
			if !verified || lv.At.Before(g.At) {
				it.Reasons = append(it.Reasons, ReasonUnverified)
				detail = append(detail, fmt.Sprintf("written by %s on %s", g.By, g.At.Format("2006-01-02")))
			}
		}
		if strings.EqualFold(c.Status, "draft") {
			it.Reasons = append(it.Reasons, ReasonDraft)
		}
		if n := len(s.Broken[p]); n > 0 {
			it.Reasons = append(it.Reasons, ReasonBroken)
			detail = append(detail, fmt.Sprintf("%d broken link(s)", n))
		}
		if ownersConfigured && len(it.Owners) == 0 {
			it.Reasons = append(it.Reasons, ReasonNoOwner)
		}
		if len(it.Reasons) == 0 {
			continue
		}
		if q.Owner != "" && !containsFold(it.Owners, q.Owner) {
			continue
		}
		if q.Reason != "" && !containsFold(it.Reasons, q.Reason) {
			continue
		}
		it.Detail = strings.Join(detail, "; ")
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].Reasons) > len(out[j].Reasons) })
	return out
}

func containsFold(xs []string, x string) bool {
	for _, s := range xs {
		if strings.EqualFold(strings.TrimPrefix(s, "@"), strings.TrimPrefix(x, "@")) {
			return true
		}
	}
	return false
}
