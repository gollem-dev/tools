package notion_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gollem-dev/tools/notion"
	"github.com/m-mizutani/gt"
)

func TestSearch(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gt.String(t, r.URL.Path).Equal("/v1/search")
		raw := gt.R1(io.ReadAll(r.Body)).NoError(t)
		gt.NoError(t, json.Unmarshal(raw, &gotBody))
		_, _ = w.Write([]byte(`{
			"results": [
				{
					"object": "page",
					"id": "page-1",
					"url": "https://notion.so/page-1",
					"last_edited_time": "2026-01-02T03:04:05.000Z",
					"properties": {"Name": {"type": "title", "title": [{"plain_text": "Hello"}]}}
				},
				{
					"object": "database",
					"id": "db-1",
					"url": "https://notion.so/db-1",
					"last_edited_time": "2026-02-03T04:05:06.000Z",
					"title": [{"plain_text": "My DB"}]
				}
			],
			"has_more": true,
			"next_cursor": "cursor-xyz"
		}`))
	}))
	defer srv.Close()

	ts := gt.R1(notion.New("tok", notion.WithBaseURL(srv.URL), notion.WithHTTPClient(srv.Client()))).NoError(t)

	res := gt.R1(ts.Run(context.Background(), "notion_search", map[string]any{
		"query":       "hello",
		"page_size":   float64(50),
		"filter_type": "page",
		"sort":        "descending",
	})).NoError(t)

	// Request shaping
	gt.Value(t, gotBody["query"]).Equal("hello")
	gt.Value(t, gotBody["page_size"]).Equal(float64(50))
	filter := gt.Cast[map[string]any](t, gotBody["filter"])
	gt.Value(t, filter["property"]).Equal("object")
	gt.Value(t, filter["value"]).Equal("page")
	sort := gt.Cast[map[string]any](t, gotBody["sort"])
	gt.Value(t, sort["direction"]).Equal("descending")

	// Response conversion
	gt.Value(t, res["has_more"]).Equal(true)
	gt.Value(t, res["next_cursor"]).Equal("cursor-xyz")
	items := gt.Cast[[]map[string]any](t, res["items"])
	gt.Array(t, items).Length(2)

	gt.Value(t, items[0]["id"]).Equal("page-1")
	gt.Value(t, items[0]["type"]).Equal("page")
	gt.Value(t, items[0]["title"]).Equal("Hello")
	gt.Value(t, items[0]["url"]).Equal("https://notion.so/page-1")
	gt.Value(t, items[0]["last_edited"]).Equal("2026-01-02T03:04:05Z")

	gt.Value(t, items[1]["id"]).Equal("db-1")
	gt.Value(t, items[1]["type"]).Equal("database")
	gt.Value(t, items[1]["title"]).Equal("My DB")
}

func TestSearchEmptyQueryDefaults(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := gt.R1(io.ReadAll(r.Body)).NoError(t)
		gt.NoError(t, json.Unmarshal(raw, &gotBody))
		_, _ = w.Write([]byte(`{"results":[],"has_more":false,"next_cursor":""}`))
	}))
	defer srv.Close()

	ts := gt.R1(notion.New("tok", notion.WithBaseURL(srv.URL), notion.WithHTTPClient(srv.Client()))).NoError(t)

	// Empty query is allowed; page_size defaults to 20; no filter/sort keys.
	_ = gt.R1(ts.Run(context.Background(), "notion_search", map[string]any{"query": ""})).NoError(t)

	gt.Value(t, gotBody["query"]).Equal("")
	gt.Value(t, gotBody["page_size"]).Equal(float64(20))
	_, hasFilter := gotBody["filter"]
	gt.Bool(t, hasFilter).False()
	_, hasSort := gotBody["sort"]
	gt.Bool(t, hasSort).False()
}

func TestSearchMissingQuery(t *testing.T) {
	ts := gt.R1(notion.New("tok")).NoError(t)
	_, err := ts.Run(context.Background(), "notion_search", map[string]any{})
	gt.Error(t, err).Contains("query is required")
}

func TestSearchClampsPageSize(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := gt.R1(io.ReadAll(r.Body)).NoError(t)
		gt.NoError(t, json.Unmarshal(raw, &gotBody))
		_, _ = w.Write([]byte(`{"results":[],"has_more":false,"next_cursor":""}`))
	}))
	defer srv.Close()

	ts := gt.R1(notion.New("tok", notion.WithBaseURL(srv.URL), notion.WithHTTPClient(srv.Client()))).NoError(t)

	_ = gt.R1(ts.Run(context.Background(), "notion_search", map[string]any{
		"query":     "x",
		"page_size": float64(500),
	})).NoError(t)

	gt.Value(t, gotBody["page_size"]).Equal(float64(100))
}
