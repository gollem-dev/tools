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

func TestQueryDatabase(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gt.String(t, r.Method).Equal(http.MethodPost)
		raw := gt.R1(io.ReadAll(r.Body)).NoError(t)
		gt.NoError(t, json.Unmarshal(raw, &gotBody))
		_, _ = w.Write([]byte(`{
			"results": [
				{
					"object": "page",
					"id": "row-1",
					"url": "https://notion.so/row-1",
					"last_edited_time": "2026-03-04T05:06:07.000Z",
					"properties": {
						"Name": {"type": "title", "title": [{"plain_text": "Task A"}]},
						"Notes": {"type": "rich_text", "rich_text": [{"plain_text": "hello"}]},
						"Priority": {"type": "number", "number": 3},
						"Status": {"type": "select", "select": {"name": "Open"}},
						"Tags": {"type": "multi_select", "multi_select": [{"name": "x"}, {"name": "y"}]},
						"Due": {"type": "date", "date": {"start": "2026-03-01", "end": ""}},
						"Done": {"type": "checkbox", "checkbox": true},
						"Link": {"type": "url", "url": "https://example.com"},
						"Empty": {"type": "number", "number": null}
					}
				}
			],
			"has_more": false,
			"next_cursor": ""
		}`))
	}))
	defer srv.Close()

	ts := gt.R1(notion.New("tok", notion.WithBaseURL(srv.URL), notion.WithHTTPClient(srv.Client()))).NoError(t)

	res := gt.R1(ts.Run(context.Background(), "notion_query_database", map[string]any{
		"database_id":  "db-42",
		"page_size":    float64(10),
		"start_cursor": "c1",
	})).NoError(t)

	gt.String(t, gotPath).Equal("/v1/databases/db-42/query")
	gt.Value(t, gotBody["page_size"]).Equal(float64(10))
	gt.Value(t, gotBody["start_cursor"]).Equal("c1")

	items := gt.Cast[[]map[string]any](t, res["items"])
	gt.Array(t, items).Length(1)

	row := items[0]
	gt.Value(t, row["id"]).Equal("row-1")
	gt.Value(t, row["title"]).Equal("Task A")
	gt.Value(t, row["last_edited"]).Equal("2026-03-04T05:06:07Z")

	props := gt.Cast[map[string]any](t, row["properties"])
	gt.Value(t, props["Name"]).Equal("Task A")
	gt.Value(t, props["Notes"]).Equal("hello")
	gt.Value(t, props["Priority"]).Equal(float64(3))
	gt.Value(t, props["Status"]).Equal("Open")
	gt.Value(t, props["Tags"]).Equal([]string{"x", "y"})
	gt.Value(t, props["Due"]).Equal("2026-03-01")
	gt.Value(t, props["Done"]).Equal(true)
	gt.Value(t, props["Link"]).Equal("https://example.com")
	// A null-valued property is omitted rather than set to nil.
	_, hasEmpty := props["Empty"]
	gt.Bool(t, hasEmpty).False()
}

func TestQueryDatabaseMissingID(t *testing.T) {
	ts := gt.R1(notion.New("tok")).NoError(t)
	_, err := ts.Run(context.Background(), "notion_query_database", map[string]any{})
	gt.Error(t, err).Contains("database_id is required")
}

func TestFlattenProperties(t *testing.T) {
	// date range renders start/end joined with a slash; formula unwraps to its inner value
	props := gt.R1(notion.FlattenPropertiesJSON(`{
		"Range": {"type": "date", "date": {"start": "2026-01-01", "end": "2026-01-31"}},
		"Email": {"type": "email", "email": "a@example.com"},
		"Formula": {"type": "formula", "formula": {"type": "string", "string": "computed"}}
	}`)).NoError(t)

	gt.Value(t, props["Range"]).Equal("2026-01-01/2026-01-31")
	gt.Value(t, props["Email"]).Equal("a@example.com")
	gt.Value(t, props["Formula"]).Equal("computed")
}
