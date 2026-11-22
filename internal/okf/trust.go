package okf

import (
	"time"
)

// Stamp is an OKF provenance record: who did something, and when.
type Stamp struct {
	By string    `json:"by"`
	At time.Time `json:"at"`
}

// List returns a frontmatter key as a string list (a YAML sequence or a
// comma-separated scalar), for keys this package does not model, such as
// `tickets` or `owners`.
func (c *Concept) List(key string) []string { return list(c.meta, key) }

// Generated returns the `generated: {by, at}` stamp, if any.
func (c *Concept) Generated() (Stamp, bool) {
	s, ok := stamp(c.Get("generated"))
	return s, ok
}

// Verified returns the `verified` stamps. OKF allows a list of `{by, at}` or a
// single mapping, which counts as a one-element list.
func (c *Concept) Verified() []Stamp {
	var out []Stamp
	switch v := c.Get("verified").(type) {
	case []any:
		for _, e := range v {
			if s, ok := stamp(e); ok {
				out = append(out, s)
			}
		}
	case map[string]any:
		if s, ok := stamp(v); ok {
			out = append(out, s)
		}
	}
	return out
}

// LastVerified returns the most recent verification, if any.
func (c *Concept) LastVerified() (Stamp, bool) {
	var best Stamp
	found := false
	for _, s := range c.Verified() {
		if !found || s.At.After(best.At) {
			best, found = s, true
		}
	}
	return best, found
}

// AddVerified appends a `{by, at}` record to `verified`, always writing a list.
func (c *Concept) AddVerified(by string, at time.Time) error {
	var list []stampYAML
	for _, s := range c.Verified() {
		list = append(list, stampYAML{s.By, s.At.Format(time.RFC3339)})
	}
	list = append(list, stampYAML{by, at.Format(time.RFC3339)})
	return c.Set("verified", list)
}

// StaleAfter returns the `stale_after` instant, if set and parseable.
// present reports whether the key exists at all.
func (c *Concept) StaleAfter() (t time.Time, present, ok bool) {
	v := c.Get("stale_after")
	if v == nil {
		return time.Time{}, false, false
	}
	t, ok = toTime(v)
	return t, true, ok
}

func stamp(v any) (Stamp, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return Stamp{}, false
	}
	by, _ := m["by"].(string)
	at, ok := toTime(m["at"])
	if !ok {
		return Stamp{}, false
	}
	return Stamp{By: by, At: at}, true
}

// toTime accepts a YAML timestamp (decoded as time.Time when unquoted) or an
// ISO 8601 string, with or without a time part.
func toTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
			if p, err := time.Parse(layout, t); err == nil {
				return p, true
			}
		}
	}
	return time.Time{}, false
}

// stampYAML writes a stamp with `by` before `at`, as the spec shows it.
type stampYAML struct {
	By string `yaml:"by"`
	At string `yaml:"at"`
}
