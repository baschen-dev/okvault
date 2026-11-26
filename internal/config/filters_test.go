package config

import (
	"net/url"
	"strings"
	"testing"
)

func TestDefaultTicketFilters(t *testing.T) {
	c, err := Parse([]byte("tracker:\n  kind: jira\n  url: https://acme.atlassian.net\n  projects: [PROJ, OPS]\n"))
	if err != nil {
		t.Fatal(err)
	}
	fs := c.TicketFilters()
	if len(fs) != 1 || fs[0].Name != FilterMine {
		t.Fatalf("without doc_label only %q: %+v", FilterMine, fs)
	}
	if want := "project in (PROJ, OPS) AND assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC"; fs[0].JQL != want {
		t.Fatalf("jql %q", fs[0].JQL)
	}

	c, err = Parse([]byte("tracker:\n  kind: jira\n  url: https://acme.atlassian.net\n  doc_label: documented\n"))
	if err != nil {
		t.Fatal(err)
	}
	fs = c.TicketFilters()
	if len(fs) != 2 || fs[1].Name != FilterMissingDocs {
		t.Fatalf("doc_label adds %q: %+v", FilterMissingDocs, fs)
	}
	// labels not in (...) alone skips tickets without any label.
	if want := `statusCategory = Done AND (labels is EMPTY OR labels not in ("documented")) ORDER BY updated DESC`; fs[1].JQL != want {
		t.Fatalf("jql %q", fs[1].JQL)
	}
	u, _ := url.Parse(c.FilterURL(fs[1].JQL))
	if u.Path != "/issues/" || u.Query().Get("jql") != fs[1].JQL {
		t.Fatalf("filter url %s", u)
	}
}

func TestConfiguredFiltersReplaceDefaults(t *testing.T) {
	c, err := Parse([]byte(`tracker:
  kind: jira
  url: https://jira.example.com
  doc_label: documented
  filters:
    - name: " Sprint "
      jql: sprint in openSprints()
    - name: Saved
      jql: filter = 12345
`))
	if err != nil {
		t.Fatal(err)
	}
	fs := c.TicketFilters()
	if len(fs) != 2 || fs[0] != (Filter{"Sprint", "sprint in openSprints()"}) || fs[1].JQL != "filter = 12345" {
		t.Fatalf("filters %+v", fs)
	}
	if none, _ := Parse([]byte("tracker:\n  kind: none\n  url: https://jira.example.com\n")); len(none.TicketFilters()) != 0 || none.FilterURL("x") != "" {
		t.Fatal("kind none has no default filters or search links")
	}
}

func TestFilterAndDocLabelErrors(t *testing.T) {
	for name, src := range map[string]string{
		"label with space": "tracker:\n  doc_label: needs doc\n",
		"label with quote": "tracker:\n  doc_label: 'a\"b'\n",
		"no jql":           "tracker:\n  filters:\n    - name: Mine\n",
		"no name":          "tracker:\n  filters:\n    - jql: x = y\n",
		"duplicate":        "tracker:\n  filters:\n    - {name: A, jql: x}\n    - {name: a, jql: y}\n",
		"unknown key":      "tracker:\n  filters:\n    - {name: A, jql: x, query: y}\n",
	} {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestJQLString(t *testing.T) {
	if got := JQLString(`a"b\c`); got != `"a\"b\\c"` {
		t.Fatalf("got %s", got)
	}
	if !strings.HasPrefix(JQLString("x"), `"`) {
		t.Fatal("not quoted")
	}
}
