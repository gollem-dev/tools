package notion_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gollem-dev/tools/notion"
	"github.com/m-mizutani/gt"
)

func TestGetPage(t *testing.T) {
	var gotPath, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotVersion = r.Header.Get("Notion-Version")
		gt.String(t, r.Method).Equal(http.MethodGet)
		_, _ = w.Write([]byte(`{"markdown":"# Title\n\nbody","truncated":true}`))
	}))
	defer srv.Close()

	ts := gt.R1(notion.New("tok", notion.WithBaseURL(srv.URL), notion.WithHTTPClient(srv.Client()))).NoError(t)

	res := gt.R1(ts.Run(context.Background(), "notion_get_page", map[string]any{
		"page_id": "abc-123",
	})).NoError(t)

	gt.String(t, gotPath).Equal("/v1/pages/abc-123/markdown")
	gt.String(t, gotVersion).Equal("2026-03-11")
	gt.Value(t, res["page_id"]).Equal("abc-123")
	gt.Value(t, res["markdown"]).Equal("# Title\n\nbody")
	gt.Value(t, res["truncated"]).Equal(true)
}

func TestGetPageMissingID(t *testing.T) {
	ts := gt.R1(notion.New("tok")).NoError(t)
	_, err := ts.Run(context.Background(), "notion_get_page", map[string]any{})
	gt.Error(t, err).Contains("page_id is required")
}

func TestGetPageHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()

	ts := gt.R1(notion.New("tok", notion.WithBaseURL(srv.URL), notion.WithHTTPClient(srv.Client()))).NoError(t)

	_, err := ts.Run(context.Background(), "notion_get_page", map[string]any{"page_id": "missing"})
	gt.Error(t, err).Contains("failed to fetch notion page markdown")
}
