package server

import (
	"sort"
	"time"

	"github.com/baschen-dev/okvault/internal/gaps"
	"github.com/baschen-dev/okvault/internal/search"
)

// PluginClient is the Obsidian plugin's MCP client name. Its kb_ask calls are
// a person typing in the FAQ panel, not an agent's question, so they are not
// recorded as gaps.
const PluginClient = "okvault-obsidian"

// GapsIn is the kb_gaps input.
type GapsIn struct {
	Limit   int    `json:"limit,omitempty" jsonschema:"max groups (default 20, max 100)"`
	Dismiss string `json:"dismiss,omitempty" jsonschema:"id of a group to hide from now on; only when a person asks for it"`
}

// NearFAQ is the FAQ that came closest to answering a gap.
type NearFAQ struct {
	Path       string  `json:"path"`
	Question   string  `json:"question"`
	Confidence float64 `json:"confidence"`
}

// Gap is a question agents asked that the vault cannot answer confidently yet,
// with the other ways they phrased it.
type Gap struct {
	ID         string       `json:"id"`
	Question   string       `json:"question" jsonschema:"the most asked phrasing still unanswered"`
	Phrasings  []string     `json:"phrasings" jsonschema:"unanswered phrasings, most asked first (aliases for the FAQ that answers them)"`
	Asked      int          `json:"asked"`
	FirstAsked time.Time    `json:"first_asked"`
	LastAsked  time.Time    `json:"last_asked"`
	Clients    []string     `json:"clients,omitempty" jsonschema:"MCP clients that asked"`
	Nearest    *NearFAQ     `json:"nearest_faq,omitempty" jsonschema:"the FAQ that came closest; a near miss often needs only an alias"`
	Related    []search.Hit `json:"related,omitempty" jsonschema:"notes agents were pointed to instead"`
}

// GapsOut is the kb_gaps result.
type GapsOut struct {
	Total     int    `json:"total" jsonschema:"open gaps (before limit)"`
	Gaps      []Gap  `json:"gaps"`
	Answered  int    `json:"answered" jsonschema:"groups an FAQ answers now (hidden)"`
	Dismissed int    `json:"dismissed" jsonschema:"groups a person dismissed (hidden)"`
	Log       string `json:"log" jsonschema:"where this machine records the questions"`
}

// maxPhrasings bounds how many phrasings of a group are re-asked.
const maxPhrasings = 8

// BuildGaps reads the log and returns the open gaps, most asked first. A
// phrasing an FAQ now answers confidently drops out; a group with none left
// counts as answered.
func BuildGaps(ix *search.Index, log *gaps.Log, limit int) (GapsOut, error) {
	out := GapsOut{Log: log.Path(), Gaps: []Gap{}}
	entries, dismissed, err := log.Read()
	if err != nil {
		return out, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	for _, g := range gaps.GroupQuestions(entries, search.Similarity, search.Normalize) {
		if dismissed[g.ID] {
			out.Dismissed++
			continue
		}
		gap := Gap{ID: g.ID, Asked: g.Asked, FirstAsked: g.First, LastAsked: g.Last, Clients: g.Clients}
		var first *search.Answer
		for i, p := range g.Phrasings {
			if i == maxPhrasings {
				break
			}
			a, err := ix.Ask(p)
			if err != nil {
				return out, err
			}
			if a.Confident {
				continue
			}
			gap.Phrasings = append(gap.Phrasings, p)
			if first == nil {
				first = a
			}
			if a.Path != "" && (gap.Nearest == nil || a.Confidence > gap.Nearest.Confidence) {
				gap.Nearest = &NearFAQ{Path: a.Path, Question: a.Question, Confidence: a.Confidence}
			}
		}
		if first == nil {
			out.Answered++
			continue
		}
		gap.Question = gap.Phrasings[0]
		gap.Related = first.Related
		if len(gap.Related) > 3 {
			gap.Related = gap.Related[:3]
		}
		out.Gaps = append(out.Gaps, gap)
	}
	sort.SliceStable(out.Gaps, func(i, j int) bool {
		a, b := out.Gaps[i], out.Gaps[j]
		if a.Asked != b.Asked {
			return a.Asked > b.Asked
		}
		return a.LastAsked.After(b.LastAsked)
	})
	out.Total = len(out.Gaps)
	if len(out.Gaps) > limit {
		out.Gaps = out.Gaps[:limit]
	}
	return out, nil
}
