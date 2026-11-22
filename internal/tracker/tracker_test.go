package tracker

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/baschen-dev/okvault/internal/config"
)

const v3Issue = `{
  "key": "PROJ-7",
  "fields": {
    "summary": "Rotate the database password",
    "issuetype": {"name": "Task"},
    "priority": {"name": "High"},
    "assignee": {"displayName": "Alice Martin"},
    "reporter": {"displayName": "Bob"},
    "status": {"name": "In Progress", "statusCategory": {"name": "In Progress"}},
    "labels": ["ops", "security"],
    "parent": {"key": "PROJ-1"},
    "updated": "2026-11-20T10:00:00.000+0000",
    "description": {"type": "doc", "version": 1, "content": [
      {"type": "paragraph", "content": [{"type": "text", "text": "Use the "}, {"type": "text", "text": "vault", "marks": [{"type": "strong"}]}, {"type": "text", "text": " CLI."}]},
      {"type": "bulletList", "content": [
        {"type": "listItem", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "staging first"}]}]},
        {"type": "listItem", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "then prod, ask "}, {"type": "mention", "attrs": {"text": "@Carol"}}]}]}
      ]},
      {"type": "codeBlock", "content": [{"type": "text", "text": "just rotate-db"}]}
    ]},
    "comment": {"comments": [
      {"author": {"displayName": "A"}, "created": "1", "body": {"type": "doc", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "one"}]}]}},
      {"author": {"displayName": "B"}, "created": "2", "body": {"type": "doc", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "two"}]}]}},
      {"author": {"displayName": "C"}, "created": "3", "body": {"type": "doc", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "three"}]}]}},
      {"author": {"displayName": "D"}, "created": "4", "body": {"type": "doc", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "four"}]}]}}
    ]}
  }
}`

func cfg(t *testing.T, url, api string) *config.Config {
	t.Helper()
	c, err := config.Parse([]byte("tracker:\n  kind: jira\n  url: " + url + "\n  api: \"" + api + "\"\n  projects: [PROJ]\n"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestJiraCloudIssue(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("me@example.com:tok"))
		if r.Header.Get("Authorization") != want {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/rest/api/3/issue/PROJ-7":
			w.Write([]byte(v3Issue))
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"errorMessages":["Issue does not exist"]}`))
		}
	}))
	defer srv.Close()
	tr, err := New(cfg(t, srv.URL, "3"), env(map[string]string{EnvEmail: "me@example.com", EnvToken: "tok"}))
	if err != nil {
		t.Fatal(err)
	}
	is, err := tr.Issue(context.Background(), "proj-7")
	if err != nil {
		t.Fatal(err)
	}
	if is.Summary != "Rotate the database password" || is.Status != "In Progress" || is.Assignee != "Alice Martin" || is.Parent != "PROJ-1" {
		t.Fatalf("fields: %+v", is)
	}
	if is.URL != srv.URL+"/browse/PROJ-7" {
		t.Fatalf("url %q", is.URL)
	}
	wantDesc := "Use the vault CLI.\n\n- staging first\n- then prod, ask @Carol\n```\njust rotate-db\n```"
	if is.Description != wantDesc {
		t.Fatalf("description:\n%q\nwant\n%q", is.Description, wantDesc)
	}
	if len(is.Comments) != 3 || is.Comments[0].Body != "two" || is.Comments[2].Author != "D" {
		t.Fatalf("comments: %+v", is.Comments)
	}
	if _, err := tr.Issue(context.Background(), "PROJ-7"); err != nil || calls.Load() != 1 {
		t.Fatalf("second call should be cached: %v, %d calls", err, calls.Load())
	}
	if _, err := tr.Issue(context.Background(), "PROJ-8"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestJiraDataCenterBearerAndV2(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer pat" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/rest/api/2/issue/PROJ-2" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"key":"PROJ-2","fields":{"summary":"S","description":"h1. Steps\n* one","status":{"name":"Done"}}}`))
	}))
	defer srv.Close()
	tr, err := New(cfg(t, srv.URL, "2"), env(map[string]string{EnvToken: "pat"}))
	if err != nil {
		t.Fatal(err)
	}
	is, err := tr.Issue(context.Background(), "PROJ-2")
	if err != nil {
		t.Fatal(err)
	}
	if is.Description != "h1. Steps\n* one" || is.Status != "Done" {
		t.Fatalf("%+v", is)
	}
	bad, _ := New(cfg(t, srv.URL, "2"), env(map[string]string{EnvToken: "wrong"}))
	if _, err := bad.Issue(context.Background(), "PROJ-2"); err == nil || !strings.Contains(err.Error(), "access denied") || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("want access denied without the token, got %v", err)
	}
}

func TestNotConfigured(t *testing.T) {
	empty, _ := config.Parse(nil)
	if _, err := New(empty, env(nil)); !errors.Is(err, ErrNotConfigured) {
		t.Fatal(err)
	}
	if _, err := New(cfg(t, "https://x.atlassian.net", "3"), env(nil)); !errors.Is(err, ErrNotConfigured) {
		t.Fatal(err)
	}
}
