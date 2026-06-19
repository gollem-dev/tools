package jira_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gollem-dev/tools/jira"
	"github.com/m-mizutani/gt"
)

// newTestServer spins up an httptest server whose handler is provided per test,
// and a ToolSet pointed at it with fixed credentials.
func newTestServer(t *testing.T, handler http.HandlerFunc) (*jira.ToolSet, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	ts, err := jira.New(srv.URL, "alice@example.com", "token-123",
		jira.WithHTTPClient(srv.Client()))
	gt.NoError(t, err)
	return ts, srv
}

func TestNew(t *testing.T) {
	t.Run("requires base URL", func(t *testing.T) {
		_, err := jira.New("", "alice@example.com", "token")
		gt.Error(t, err)
	})
	t.Run("requires email", func(t *testing.T) {
		_, err := jira.New("https://x.atlassian.net", "", "token")
		gt.Error(t, err)
	})
	t.Run("requires api token", func(t *testing.T) {
		_, err := jira.New("https://x.atlassian.net", "alice@example.com", "")
		gt.Error(t, err)
	})
	t.Run("rejects non-absolute base URL", func(t *testing.T) {
		_, err := jira.New("not-a-url", "alice@example.com", "token")
		gt.Error(t, err)
	})
	t.Run("accepts valid config", func(t *testing.T) {
		ts, err := jira.New("https://x.atlassian.net/", "alice@example.com", "token")
		gt.NoError(t, err)
		gt.NotNil(t, ts)
	})
}

func TestSpecs(t *testing.T) {
	ts, err := jira.New("https://x.atlassian.net", "alice@example.com", "token")
	gt.NoError(t, err)
	specs, err := ts.Specs(context.Background())
	gt.NoError(t, err)
	gt.A(t, specs).Length(3)

	names := map[string]bool{}
	for _, s := range specs {
		names[s.Name] = true
	}
	gt.True(t, names["jira_list_projects"])
	gt.True(t, names["jira_search_issues"])
	gt.True(t, names["jira_get_issues"])
}

func TestPing(t *testing.T) {
	t.Run("succeeds and sends basic auth", func(t *testing.T) {
		ts, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			gt.Equal(t, r.URL.Path, "/rest/api/3/myself")
			want := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice@example.com:token-123"))
			gt.Equal(t, r.Header.Get("Authorization"), want)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"accountId":"1"}`))
		})
		gt.NoError(t, ts.Ping(context.Background()))
	})

	t.Run("propagates non-2xx as error", func(t *testing.T) {
		ts, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"errorMessages":["bad creds"]}`))
		})
		gt.Error(t, ts.Ping(context.Background()))
	})
}

func TestRunUnknownTool(t *testing.T) {
	ts, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {})
	_, err := ts.Run(context.Background(), "jira_nope", nil)
	gt.Error(t, err)
}

// TestLive hits the real Jira Cloud API. It runs only when TEST_JIRA_BASE_URL,
// TEST_JIRA_EMAIL and TEST_JIRA_API_TOKEN are all set. TEST_JIRA_PROJECT and
// TEST_JIRA_ISSUE_KEY enable the search and get-issues subtests respectively.
func TestLive(t *testing.T) {
	baseURL, ok := os.LookupEnv("TEST_JIRA_BASE_URL")
	if !ok {
		t.Skip("TEST_JIRA_BASE_URL is not set")
	}
	email, ok := os.LookupEnv("TEST_JIRA_EMAIL")
	if !ok {
		t.Skip("TEST_JIRA_EMAIL is not set")
	}
	token, ok := os.LookupEnv("TEST_JIRA_API_TOKEN")
	if !ok {
		t.Skip("TEST_JIRA_API_TOKEN is not set")
	}

	ts := gt.R1(jira.New(baseURL, email, token)).NoError(t)
	gt.NoError(t, ts.Ping(context.Background())).Required()

	t.Run("jira_list_projects", func(t *testing.T) {
		res := gt.R1(ts.Run(context.Background(), "jira_list_projects", map[string]any{})).NoError(t)
		gt.Map(t, res).HasKey("items")
		_ = gt.R1(json.Marshal(res)).NoError(t)
	})

	t.Run("jira_search_issues", func(t *testing.T) {
		project, ok := os.LookupEnv("TEST_JIRA_PROJECT")
		if !ok {
			t.Skip("TEST_JIRA_PROJECT is not set")
		}
		res := gt.R1(ts.Run(context.Background(), "jira_search_issues", map[string]any{
			"project": project,
		})).NoError(t)
		gt.Map(t, res).HasKey("items")
		_ = gt.R1(json.Marshal(res)).NoError(t)
	})

	t.Run("jira_get_issues", func(t *testing.T) {
		key, ok := os.LookupEnv("TEST_JIRA_ISSUE_KEY")
		if !ok {
			t.Skip("TEST_JIRA_ISSUE_KEY is not set")
		}
		res := gt.R1(ts.Run(context.Background(), "jira_get_issues", map[string]any{
			"issue_keys":       []any{key},
			"include_comments": true,
		})).NoError(t)
		gt.Map(t, res).HasKey("items")
		_ = gt.R1(json.Marshal(res)).NoError(t)
	})
}
