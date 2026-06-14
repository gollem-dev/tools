package notion_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gollem-dev/tools/notion"
	"github.com/m-mizutani/gt"
)

func TestNewRequiresToken(t *testing.T) {
	_, err := notion.New("")
	gt.Error(t, err).Contains("token")
}

func TestSpecs(t *testing.T) {
	ts := gt.R1(notion.New("dummy")).NoError(t)

	specs := gt.R1(ts.Specs(context.Background())).NoError(t)
	gt.Array(t, specs).Length(3)

	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
	}
	gt.Array(t, names).
		Has("notion_search").
		Has("notion_get_page").
		Has("notion_query_database")
}

func TestRunInvalidName(t *testing.T) {
	ts := gt.R1(notion.New("dummy")).NoError(t)
	_, err := ts.Run(context.Background(), "notion_unknown", map[string]any{})
	gt.Error(t, err).Contains("invalid function name")
}

func TestPing(t *testing.T) {
	var gotPath, gotAuth, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("Notion-Version")
		_, _ = w.Write([]byte(`{"results":[],"has_more":false,"next_cursor":""}`))
	}))
	defer srv.Close()

	ts := gt.R1(notion.New(
		"secret-token",
		notion.WithBaseURL(srv.URL),
		notion.WithHTTPClient(srv.Client()),
	)).NoError(t)

	gt.NoError(t, ts.Ping(context.Background()))
	gt.String(t, gotPath).Equal("/v1/search")
	gt.String(t, gotAuth).Equal("Bearer secret-token")
	gt.String(t, gotVersion).Equal("2022-06-28")
}

func TestPingError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"unauthorized"}`))
	}))
	defer srv.Close()

	ts := gt.R1(notion.New(
		"bad",
		notion.WithBaseURL(srv.URL),
		notion.WithHTTPClient(srv.Client()),
	)).NoError(t)

	gt.Error(t, ts.Ping(context.Background())).Contains("Notion ping failed")
}

// TestLive hits the real Notion API. It runs only when TEST_NOTION_TOKEN is set.
// TEST_NOTION_PAGE_ID and TEST_NOTION_DATABASE_ID enable the per-tool subtests.
func TestLive(t *testing.T) {
	token, ok := os.LookupEnv("TEST_NOTION_TOKEN")
	if !ok {
		t.Skip("TEST_NOTION_TOKEN is not set")
	}

	ts := gt.R1(notion.New(token)).NoError(t)
	gt.NoError(t, ts.Ping(context.Background())).Required()

	t.Run("notion_search", func(t *testing.T) {
		res := gt.R1(ts.Run(context.Background(), "notion_search", map[string]any{"query": ""})).NoError(t)
		gt.Map(t, res).HasKey("items")
		_ = gt.R1(json.Marshal(res)).NoError(t)
	})

	t.Run("notion_get_page", func(t *testing.T) {
		pageID, ok := os.LookupEnv("TEST_NOTION_PAGE_ID")
		if !ok {
			t.Skip("TEST_NOTION_PAGE_ID is not set")
		}
		res := gt.R1(ts.Run(context.Background(), "notion_get_page", map[string]any{"page_id": pageID})).NoError(t)
		gt.Map(t, res).HasKey("markdown")
		_ = gt.R1(json.Marshal(res)).NoError(t)
	})

	t.Run("notion_query_database", func(t *testing.T) {
		dbID, ok := os.LookupEnv("TEST_NOTION_DATABASE_ID")
		if !ok {
			t.Skip("TEST_NOTION_DATABASE_ID is not set")
		}
		res := gt.R1(ts.Run(context.Background(), "notion_query_database", map[string]any{"database_id": dbID})).NoError(t)
		gt.Map(t, res).HasKey("items")
		_ = gt.R1(json.Marshal(res)).NoError(t)
	})
}
