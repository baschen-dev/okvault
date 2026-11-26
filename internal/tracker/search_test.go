package tracker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const searchResult = `{"issues":[
  {"key":"PROJ-7","fields":{"summary":"Rotate the database password","status":{"name":"Done","statusCategory":{"name":"Done"}},"labels":[]}},
  {"key":"PROJ-8","fields":{"summary":"Add a runbook","assignee":{"displayName":"Alice"},"status":{"name":"To Do"}}}
],"isLast":true}`

func TestSearchEndpoints(t *testing.T) {
	for api, path := range map[string]string{"3": "/rest/api/3/search/jql", "2": "/rest/api/2/search"} {
		var got *http.Request
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r
			if r.URL.Path != path {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write([]byte(searchResult))
		}))
		tr, err := New(cfg(t, srv.URL, api), env(map[string]string{EnvToken: "pat"}))
		if err != nil {
			t.Fatal(err)
		}
		jql := `assignee = currentUser() AND labels not in ("doc")`
		is, err := tr.Search(context.Background(), jql, 0)
		srv.Close()
		if err != nil {
			t.Fatalf("api %s: %v", api, err)
		}
		q := got.URL.Query()
		if q.Get("jql") != jql || q.Get("maxResults") != "50" || strings.Contains(q.Get("fields"), "description") {
			t.Fatalf("api %s: query %v", api, q)
		}
		if got.Header.Get("Authorization") != "Bearer pat" {
			t.Fatalf("api %s: no auth", api)
		}
		if len(is) != 2 || is[0].Key != "PROJ-7" || is[0].Category != "Done" || is[1].Assignee != "Alice" {
			t.Fatalf("api %s: issues %+v", api, is)
		}
		if is[0].URL != srv.URL+"/browse/PROJ-7" {
			t.Fatalf("api %s: url %q", api, is[0].URL)
		}
	}
}

func TestSearchLimitAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("jql") == "bad" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"errorMessages":["Field 'bad' does not exist."]}`))
			return
		}
		if r.URL.Query().Get("jql") == "denied" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(searchResult))
	}))
	defer srv.Close()
	tr, _ := New(cfg(t, srv.URL, "3"), env(map[string]string{EnvToken: "pat"}))
	is, err := tr.Search(context.Background(), "x", 1)
	if err != nil || len(is) != 1 {
		t.Fatalf("limit 1: %v %+v", err, is)
	}
	if _, err := tr.Search(context.Background(), "bad", 5); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("bad jql: %v", err)
	}
	if _, err := tr.Search(context.Background(), "denied", 5); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("denied: %v", err)
	}
}
