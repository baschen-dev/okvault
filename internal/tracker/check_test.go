package tracker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// fakeJira serves /myself and paged /project/search (v3) or /project (v2).
func fakeJira(t *testing.T, wantAuth string, keys []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != wantAuth {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"errorMessages":["You are not authenticated."]}`)
			return
		}
		switch r.URL.Path {
		case "/rest/api/3/myself", "/rest/api/2/myself":
			fmt.Fprint(w, `{"displayName":"Ada Lovelace"}`)
		case "/rest/api/3/project/search":
			start, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
			end := min(start+2, len(keys)) // pages of two
			var vals []map[string]string
			for _, k := range keys[start:end] {
				vals = append(vals, map[string]string{"key": k, "name": "Project " + k})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"values": vals, "isLast": end == len(keys)})
		case "/rest/api/2/project":
			var vals []map[string]string
			for _, k := range keys {
				vals = append(vals, map[string]string{"key": k, "name": "Project " + k})
			}
			_ = json.NewEncoder(w).Encode(vals)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestCheckJiraCloudPages(t *testing.T) {
	const tok = "s3cret-token"
	srv := fakeJira(t, "Basic "+b64("ada@example.com:"+tok), []string{"OPS", "PROJ", "web-legacy", "DATA"})
	defer srv.Close()
	c := CheckJira(context.Background(), srv.URL+"/", "3", envOf(map[string]string{EnvToken: tok, EnvEmail: "ada@example.com"}), srv.Client())
	if c.Problem != "" || c.User != "Ada Lovelace" || c.Credentials != "JIRA_API_TOKEN + JIRA_EMAIL" {
		t.Fatalf("check: %+v", c)
	}
	var keys []string
	for _, p := range c.Projects {
		keys = append(keys, p.Key)
	}
	if strings.Join(keys, ",") != "DATA,OPS,PROJ" { // every page, sorted, unusable key dropped
		t.Errorf("projects: %v", keys)
	}
	b, _ := json.Marshal(c)
	if strings.Contains(string(b), tok) {
		t.Error("the token leaked into the result")
	}
}

func TestCheckJiraDataCenter(t *testing.T) {
	srv := fakeJira(t, "Bearer pat", []string{"OPS"})
	defer srv.Close()
	c := CheckJira(context.Background(), srv.URL, "", envOf(map[string]string{EnvToken: "pat"}), srv.Client())
	if c.API != "2" || c.Problem != "" || len(c.Projects) != 1 || !strings.Contains(c.Credentials, "personal access token") {
		t.Fatalf("check: %+v", c)
	}
}

func TestCheckJiraProblems(t *testing.T) {
	srv := fakeJira(t, "Bearer right", nil)
	defer srv.Close()
	ctx := context.Background()
	if c := CheckJira(ctx, srv.URL, "", envOf(nil), srv.Client()); c.Credentials != "none" || !strings.Contains(c.Problem, EnvToken) {
		t.Errorf("no token: %+v", c)
	}
	if c := CheckJira(ctx, srv.URL, "", envOf(map[string]string{EnvToken: "wrong"}), srv.Client()); !strings.Contains(c.Problem, "HTTP 401") || !strings.Contains(c.Problem, "not authenticated") {
		t.Errorf("bad token: %+v", c)
	}
	if c := CheckJira(ctx, srv.URL, "", envOf(map[string]string{EnvToken: "right"}), srv.Client()); !strings.Contains(c.Problem, "see no projects") || c.User == "" {
		t.Errorf("no projects: %+v", c)
	}
	if c := CheckJira(ctx, "ftp://x", "", envOf(nil), nil); !strings.Contains(c.Problem, "not a Jira URL") {
		t.Errorf("bad url: %+v", c)
	}
	if c := CheckJira(ctx, "https://me:pw@x.atlassian.net", "", envOf(nil), nil); !strings.Contains(c.Problem, "must not contain credentials") {
		t.Errorf("creds in url: %+v", c)
	}
}

func TestCheckJiraCloudWithoutEmailExplains(t *testing.T) {
	// A *.atlassian.net site answering 401 to a bearer token: point at JIRA_EMAIL.
	rt := roundTrip(func(r *http.Request) *http.Response {
		return &http.Response{StatusCode: http.StatusUnauthorized, Body: http.NoBody, Header: http.Header{}, Request: r}
	})
	c := CheckJira(context.Background(), "https://acme.atlassian.net", "", envOf(map[string]string{EnvToken: "tok"}), &http.Client{Transport: rt})
	if c.API != "3" || !strings.Contains(c.Problem, "Jira Cloud needs JIRA_EMAIL") {
		t.Fatalf("check: %+v", c)
	}
}

type roundTrip func(*http.Request) *http.Response

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r), nil }

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
