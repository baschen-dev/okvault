// Package search keeps an in-memory BM25 index (bleve) over a vault snapshot
// and a FAQ fast path for question-style lookups.
package search

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
	"github.com/blevesearch/bleve/v2/analysis/lang/en"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"

	"github.com/baschen-dev/okvault/internal/config"
	"github.com/baschen-dev/okvault/internal/okf"
	"github.com/baschen-dev/okvault/internal/vault"
)

// Field boosts: metadata matches matter more than body matches.
var boosts = map[string]float64{"title": 4, "aliases": 3, "description": 2, "tags": 2, "body": 1}

type doc struct {
	Path        string   `json:"path"`
	Type        string   `json:"type"`
	TypeText    string   `json:"type_text"`
	Tags        []string `json:"tags"`
	TagsText    string   `json:"tags_text"`
	Title       string   `json:"title"`
	Aliases     string   `json:"aliases"`
	Description string   `json:"description"`
	Body        string   `json:"body"`
	Status      string   `json:"status"`
	Tickets     []string `json:"tickets"`
	Owners      []string `json:"owners"`
}

// Index is a swappable in-memory search index.
type Index struct {
	mu   sync.RWMutex
	idx  bleve.Index
	snap *vault.Snapshot
	faq  []faqEntry
}

type faqEntry struct {
	Path     string
	Question string
	norm     string
	tokens   map[string]bool
	draft    bool // status: draft; never a confident answer, still the nearest FAQ
}

func newMapping() mapping.IndexMapping {
	m := bleve.NewIndexMapping()
	m.ScoringModel = "bm25"
	m.DefaultAnalyzer = en.AnalyzerName
	dm := bleve.NewDocumentMapping()
	kw := bleve.NewTextFieldMapping()
	kw.Analyzer = keyword.Name
	kw.IncludeTermVectors = false
	txt := func() *mapping.FieldMapping {
		f := bleve.NewTextFieldMapping()
		f.Analyzer = en.AnalyzerName
		f.Store = false
		return f
	}
	dm.AddFieldMappingsAt("path", kw)
	dm.AddFieldMappingsAt("type", kw)
	dm.AddFieldMappingsAt("tags", kw)
	dm.AddFieldMappingsAt("status", kw)
	dm.AddFieldMappingsAt("tickets", kw)
	dm.AddFieldMappingsAt("owners", kw)
	for _, f := range []string{"type_text", "tags_text", "title", "aliases", "description", "body"} {
		dm.AddFieldMappingsAt(f, txt())
	}
	m.DefaultMapping = dm
	return m
}

// Build creates an index over a snapshot.
func Build(s *vault.Snapshot) (*Index, error) {
	ix := &Index{}
	return ix, ix.Rebuild(s)
}

// Rebuild replaces the index contents with a new snapshot.
func (ix *Index) Rebuild(s *vault.Snapshot) error {
	idx, err := bleve.NewMemOnly(newMapping())
	if err != nil {
		return err
	}
	b := idx.NewBatch()
	var faq []faqEntry
	for _, p := range s.Sorted() {
		c := s.Concepts[p]
		tags := lowerAll(c.Tags)
		if err := b.Index(p, doc{
			Path: p, Type: strings.ToLower(c.Type), TypeText: c.Type,
			Tags: tags, TagsText: strings.Join(c.Tags, " "),
			Title: c.Title, Aliases: strings.Join(c.Aliases, "\n"),
			Description: c.Description, Body: c.Body, Status: strings.ToLower(c.Status),
			Tickets: s.NoteTickets[p], Owners: normOwners(s.Owners(p)),
		}); err != nil {
			return err
		}
		// Deprecated FAQs answer nothing; drafts are not answers yet (an
		// answer still being written must not reach agents as one).
		if st := strings.ToLower(c.Status); IsFAQ(c) && st != "deprecated" {
			for _, q := range append([]string{c.Title}, c.Aliases...) {
				if n := normalize(q); n != "" {
					faq = append(faq, faqEntry{Path: p, Question: q, norm: n, tokens: terms(q), draft: st == "draft"})
				}
			}
		}
	}
	if err := idx.Batch(b); err != nil {
		return err
	}
	ix.mu.Lock()
	old := ix.idx
	ix.idx, ix.snap, ix.faq = idx, s, faq
	ix.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	return nil
}

// IsFAQ reports whether a concept is a question/answer entry.
func IsFAQ(c *okf.Concept) bool {
	t := strings.ToLower(c.Type)
	return t == "faq" || t == "question"
}

// Hit is a compact search result.
type Hit struct {
	Path        string   `json:"path"`
	Title       string   `json:"title"`
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Tickets     []string `json:"tickets,omitempty"`
	Snippet     string   `json:"snippet,omitempty"`
	Score       float64  `json:"score"`
}

// Query is a search request.
type Query struct {
	Text           string
	Type           string
	Tags           []string
	Dir            string
	Ticket         string // only notes linked to or mentioning this ticket key
	Owner          string // only notes owned by this person or group
	Limit          int
	WithDeprecated bool
}

// Search runs a BM25 query with optional type/tag/dir filters.
func (ix *Index) Search(q Query) ([]Hit, error) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	if q.Limit <= 0 || q.Limit > 50 {
		q.Limit = 8
	}
	var must []query.Query
	text := strings.TrimSpace(q.Text)
	if text == "" {
		must = append(must, bleve.NewMatchAllQuery())
	} else {
		var should []query.Query
		for f, boost := range boosts {
			mq := bleve.NewMatchQuery(text)
			mq.SetField(f)
			mq.SetBoost(boost)
			should = append(should, mq)
			if f == "title" || f == "body" {
				// tolerate small typos, at a lower weight
				fq := bleve.NewMatchQuery(text)
				fq.SetField(f)
				fq.SetFuzziness(1)
				fq.SetBoost(boost * 0.2)
				should = append(should, fq)
			}
		}
		pq := bleve.NewMatchPhraseQuery(text)
		pq.SetField("body")
		pq.SetBoost(2)
		should = append(should, pq)
		tq := bleve.NewMatchQuery(text)
		tq.SetField("type_text")
		should = append(should, tq)
		// the English analyzer splits PROJ-123 into "proj" and "123": match keys exactly
		for _, w := range strings.Fields(text) {
			if k, ok := config.NormalizeKey(strings.Trim(w, ".,;:!?()[]\"'")); ok {
				kq := bleve.NewTermQuery(k)
				kq.SetField("tickets")
				kq.SetBoost(5)
				should = append(should, kq)
			}
		}
		must = append(must, bleve.NewDisjunctionQuery(should...))
	}
	if q.Type != "" {
		tq := bleve.NewTermQuery(strings.ToLower(q.Type))
		tq.SetField("type")
		must = append(must, tq)
	}
	for _, t := range q.Tags {
		tq := bleve.NewTermQuery(strings.ToLower(t))
		tq.SetField("tags")
		must = append(must, tq)
	}
	if q.Ticket != "" {
		k, _ := config.NormalizeKey(q.Ticket)
		kq := bleve.NewTermQuery(k)
		kq.SetField("tickets")
		must = append(must, kq)
	}
	if q.Owner != "" {
		oq := bleve.NewTermQuery(normOwners([]string{q.Owner})[0])
		oq.SetField("owners")
		must = append(must, oq)
	}
	if d := strings.Trim(q.Dir, "/"); d != "" && d != "." {
		pq := bleve.NewPrefixQuery(d + "/")
		pq.SetField("path")
		must = append(must, pq)
	}
	var root query.Query = bleve.NewConjunctionQuery(must...)
	if !q.WithDeprecated {
		dep := bleve.NewTermQuery("deprecated")
		dep.SetField("status")
		bq := bleve.NewBooleanQuery()
		bq.AddMust(root)
		bq.AddMustNot(dep)
		root = bq
	}
	req := bleve.NewSearchRequestOptions(root, q.Limit, 0, false)
	if text == "" {
		req.SortBy([]string{"path"})
	}
	res, err := ix.idx.Search(req)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	hits := make([]Hit, 0, len(res.Hits))
	for _, h := range res.Hits {
		c, ok := ix.snap.Concepts[h.ID]
		if !ok {
			continue
		}
		hits = append(hits, Hit{
			Path: h.ID, Title: c.Title, Type: c.Type, Description: c.Description, Tags: c.Tags,
			Tickets: ix.snap.NoteTickets[h.ID],
			Snippet: Snippet(c.Body, text, 240), Score: round(h.Score),
		})
	}
	return hits, nil
}

// Answer is the result of the FAQ fast path.
type Answer struct {
	Confident  bool    `json:"confident"`
	Confidence float64 `json:"confidence"`
	Path       string  `json:"path,omitempty"`
	Question   string  `json:"matched_question,omitempty"`
	Answer     string  `json:"answer,omitempty"`
	Related    []Hit   `json:"related,omitempty"`
}

// Thresholds for a confident FAQ answer (see vault/faq/how-does-kb-ask-decide.md).
const (
	Threshold   = 0.6  // minimum Dice overlap between query and FAQ question terms
	MinCoverage = 0.75 // minimum share of the query's terms found in the FAQ question
)

// Ask answers a question from FAQ entries when it is confident; otherwise it
// returns the best search hits so the agent can read further.
//
// Confident means: the normalised question equals an FAQ title or alias, or
// the Dice overlap of analysed terms is >= Threshold AND the FAQ question
// covers >= MinCoverage of the query's terms. Coverage stops a one-keyword
// FAQ ("What is okvault?") from capturing "what is the okvault license?".
func (ix *Index) Ask(question string) (*Answer, error) {
	n := normalize(question)
	qt := terms(question)
	ix.mu.RLock()
	best, bestScore, bestOK := faqEntry{}, 0.0, false
	for _, f := range ix.faq {
		var dice float64
		ok := false
		if f.norm == n {
			dice, ok = 1, true
		} else {
			var cov float64
			dice, cov = overlap(qt, f.tokens)
			ok = dice >= Threshold && cov >= MinCoverage
		}
		ok = ok && !f.draft
		if (ok && !bestOK) || (ok == bestOK && dice > bestScore) {
			best, bestScore, bestOK = f, dice, ok
		}
	}
	snap := ix.snap
	ix.mu.RUnlock()

	if bestOK {
		c := snap.Concepts[best.Path]
		return &Answer{Confident: true, Confidence: round(bestScore), Path: best.Path, Question: best.Question, Answer: strings.TrimSpace(c.Body)}, nil
	}
	hits, err := ix.Search(Query{Text: question, Limit: 5})
	if err != nil {
		return nil, err
	}
	a := &Answer{Confident: false, Confidence: round(bestScore), Related: hits}
	if bestScore > 0 {
		a.Path, a.Question = best.Path, best.Question
	}
	return a, nil
}

// Snippet returns the line of body that best matches the query terms.
func Snippet(body, q string, max int) string {
	qt := terms(q)
	lines := strings.Split(body, "\n")
	bestLine, bestN := "", 0
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "```") {
			continue
		}
		lt := terms(t)
		n := 0
		for term := range qt {
			if lt[term] {
				n++
			}
		}
		if n > bestN {
			bestLine, bestN = t, n
		}
	}
	if bestLine == "" {
		for _, l := range lines {
			if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") {
				bestLine = t
				break
			}
		}
	}
	if r := []rune(bestLine); len(r) > max {
		bestLine = string(r[:max]) + "…"
	}
	return bestLine
}

// enAnalyzer is the same English analyzer the index uses (stop words + Porter stemming),
// so FAQ matching and search agree on what a term is.
var enAnalyzer = bleve.NewIndexMapping().AnalyzerNamed(en.AnalyzerName)

func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// terms returns the set of analysed content terms of s.
func terms(s string) map[string]bool {
	out := map[string]bool{}
	for _, tok := range enAnalyzer.Analyze([]byte(s)) {
		out[string(tok.Term)] = true
	}
	return out
}

// overlap returns the Dice coefficient of q and f and the share of q found in f.
func overlap(q, f map[string]bool) (dice, coverage float64) {
	if len(q) == 0 || len(f) == 0 {
		return 0, 0
	}
	inter := 0
	for t := range q {
		if f[t] {
			inter++
		}
	}
	return 2 * float64(inter) / float64(len(q)+len(f)), float64(inter) / float64(len(q))
}

func lowerAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(s)
	}
	return out
}

func round(f float64) float64 { return float64(int(f*1000+0.5)) / 1000 }

// Facets summarises types and tags in the snapshot.
func Facets(s *vault.Snapshot) (types, tags map[string]int) {
	types, tags = map[string]int{}, map[string]int{}
	for _, c := range s.Concepts {
		types[c.Type]++
		for _, t := range c.Tags {
			tags[t]++
		}
	}
	return
}

// TopKeys returns map keys sorted by count desc then name.
func TopKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if m[out[i]] != m[out[j]] {
			return m[out[i]] > m[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// normOwners lower-cases owner names and drops a leading "@", so "@Alice"
// and "alice" match.
func normOwners(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "@"))
	}
	return out
}

// Normalize lower-cases s and keeps only letters and digits, single-spaced: the
// form kb_ask compares FAQ titles and aliases in.
func Normalize(s string) string { return normalize(s) }

// Similarity is the Dice overlap of the analysed terms of a and b (0..1), the
// measure kb_ask matches questions with.
func Similarity(a, b string) float64 {
	d, _ := overlap(terms(a), terms(b))
	return d
}

// HasTerms reports whether s has any content terms once stop words are dropped.
func HasTerms(s string) bool { return len(terms(s)) > 0 }
