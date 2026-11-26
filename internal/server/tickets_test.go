package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTicketFilters(t *testing.T) {
	var jql string
	jira := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/search/jql" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jql = r.URL.Query().Get("jql")
		w.Write([]byte(`{"issues":[
			{"key":"PROJ-7","fields":{"summary":"Rotate the database password","status":{"name":"Done"}}},
			{"key":"PROJ-9","fields":{"summary":"Undocumented","status":{"name":"Done"}}}]}`))
	}))
	defer jira.Close()
	files := teamFiles(jira.URL)
	files[".okvault.yaml"] = strings.Replace(files[".okvault.yaml"], "projects: [PROJ]\n", "projects: [PROJ]\n  doc_label: documented\n", 1)
	cs, _ := connectTeam(t, files, map[string]string{"JIRA_API_TOKEN": "pat"})

	var list TicketsOut
	call(t, cs, "kb_tickets", map[string]any{}, &list)
	if len(list.Filters) != 2 || list.Filters[0].Name != "My tickets" || list.Filters[1].Name != "Missing docs" || list.Tickets != nil {
		t.Fatalf("filters %+v", list)
	}
	if !strings.HasPrefix(list.Filters[1].URL, jira.URL+"/issues/?jql=") {
		t.Fatalf("filter url %q", list.Filters[1].URL)
	}

	var out TicketsOut
	call(t, cs, "kb_tickets", map[string]any{"filter": "missing docs"}, &out)
	if out.Filter == nil || out.Filter.Name != "Missing docs" || jql != out.Filter.JQL || out.TrackerNote != "" {
		t.Fatalf("ran %q: %+v", jql, out)
	}
	if len(out.Tickets) != 2 || out.Tickets[0].Key != "PROJ-7" || out.Tickets[0].Linked != 1 || out.Tickets[0].Mentioned != 1 || out.Tickets[1].Linked != 0 {
		t.Fatalf("tickets %+v", out.Tickets)
	}

	res := call(t, cs, "kb_tickets", map[string]any{"filter": "nope"}, nil)
	if !res.IsError || !strings.Contains(text(res), "My tickets, Missing docs") {
		t.Fatalf("unknown filter: %s", text(res))
	}
}

func TestTicketFiltersWithoutCredentials(t *testing.T) {
	cs, _ := connectTeam(t, teamFiles("https://jira.example.com"), nil)
	var out TicketsOut
	call(t, cs, "kb_tickets", map[string]any{"filter": "My tickets"}, &out)
	if out.Filter == nil || out.Tickets != nil || !strings.Contains(out.TrackerNote, "JIRA_API_TOKEN") {
		t.Fatalf("%+v", out)
	}
}
