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
		}); err != nil {
			return err
		}
		if IsFAQ(c) {
			for _, q := range append([]string{c.Title}, c.Aliases...) {
				if n := normalize(q); n != "" {
					faq = append(faq, faqEntry{Path: p, Question: q, norm: n, tokens: tokenSet(n)})
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
	Snippet     string   `json:"snippet,omitempty"`
	Score       float64  `json:"score"`
}

// Query is a search request.
type Query struct {
	Text           string
	Type           string
	Tags           []string
	Dir            string
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

// Threshold is the minimum question similarity for a confident FAQ answer.
const Threshold = 0.6

// Ask answers a question from FAQ entries when it is confident; otherwise it
// returns the best search hits so the agent can read further.
func (ix *Index) Ask(question string) (*Answer, error) {
	n := normalize(question)
	qt := tokenSet(n)
	ix.mu.RLock()
	best, bestScore := faqEntry{}, 0.0
	for _, f := range ix.faq {
		var s float64
		if f.norm == n {
			s = 1
		} else {
			s = similarity(qt, f.tokens)
		}
		if s > bestScore {
			best, bestScore = f, s
		}
	}
	snap := ix.snap
	ix.mu.RUnlock()

	if bestScore >= Threshold {
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
	terms := strings.Fields(normalize(q))
	lines := strings.Split(body, "\n")
	bestLine, bestN := "", 0
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "```") {
			continue
		}
		ll := strings.ToLower(t)
		n := 0
		for _, term := range terms {
			if len(term) > 2 && strings.Contains(ll, stem(term)) {
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

var stop = map[string]bool{
	"a": true, "an": true, "the": true, "is": true, "are": true, "do": true, "does": true, "i": true,
	"to": true, "of": true, "in": true, "on": true, "for": true, "and": true, "or": true, "how": true,
	"what": true, "can": true, "we": true, "you": true, "my": true, "our": true, "it": true, "with": true,
	"should": true, "be": true, "which": true, "where": true, "when": true, "why": true, "this": true,
	"that": true, "there": true, "about": true, "me": true, "use": false,
}

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

func stem(t string) string {
	for _, suf := range []string{"ing", "ies", "es", "s", "ed"} {
		if len(t) > len(suf)+2 && strings.HasSuffix(t, suf) {
			return strings.TrimSuffix(t, suf)
		}
	}
	return t
}

func tokenSet(norm string) map[string]bool {
	out := map[string]bool{}
	for _, t := range strings.Fields(norm) {
		if !stop[t] {
			out[stem(t)] = true
		}
	}
	return out
}

// similarity is the Dice coefficient over content tokens.
func similarity(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for t := range a {
		if b[t] {
			inter++
		}
	}
	return 2 * float64(inter) / float64(len(a)+len(b))
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
