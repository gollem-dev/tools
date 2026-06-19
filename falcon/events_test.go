package falcon_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gollem-dev/tools/falcon"
	"github.com/m-mizutani/gt"
)

// eventsHandler builds a mock NGSIEM handler: it creates a query job and, on
// each poll, returns the given events with the given done flag.
func eventsHandler(t *testing.T, events []any, done bool) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/queryjobs"):
			writeJSON(t, w, map[string]any{"id": "job-1"})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/queryjobs/"):
			resp := map[string]any{"events": events, "done": done}
			if done {
				resp["metadataResult"] = map[string]any{"eventCount": len(events)}
			}
			writeJSON(t, w, resp)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}
}

// TestSearchEventsCapsAndPaginates verifies that when a single poll already
// exceeds maxFetchRecords, the result is truncated, capped, and the overflow is
// served from memory.
func TestSearchEventsCapsAndPaginates(t *testing.T) {
	events := make([]any, 0, 5)
	for i := range 5 {
		events = append(events, map[string]any{"n": fmt.Sprintf("evt-%d", i)})
	}
	ts, calls := newToolSet(t, eventsHandler(t, events, false),
		falcon.WithMaxFetchRecords(4), falcon.WithMaxRecords(2))
	ctx := context.Background()

	res := gt.R1(ts.Run(ctx, "falcon_search_events", map[string]any{"query_string": "aid=abc"})).NoError(t)
	gt.Value(t, res["total"]).Equal(4) // capped at maxFetchRecords
	gt.Value(t, res["count"]).Equal(2) // first page
	gt.Value(t, res["has_more"]).Equal(true)
	gt.Value(t, res["truncated"]).Equal(true)
	gt.Value(t, res["done"]).Equal(false)
	gt.Value(t, res["repository"]).Equal("search-all")
	// NGSIEM has no grand total, so total_available is omitted.
	_, hasTotalAvailable := res["total_available"]
	gt.False(t, hasTotalAvailable)

	createPlusOnePoll := 2
	gt.Number(t, *calls).Equal(createPlusOnePoll)

	// Remaining events come from memory: no extra API calls.
	token := gt.Cast[string](t, res["page_token"])
	res2 := gt.R1(ts.Run(ctx, "falcon_search_events", map[string]any{"page_token": token})).NoError(t)
	gt.Array(t, res2["records"].([]any)).Length(2)
	gt.Value(t, res2["has_more"]).Equal(false)
	gt.Number(t, *calls).Equal(createPlusOnePoll)
}

// TestSearchEventsCompletes verifies the done path returns all events untruncated.
func TestSearchEventsCompletes(t *testing.T) {
	events := []any{map[string]any{"n": "a"}, map[string]any{"n": "b"}}
	ts, _ := newToolSet(t, eventsHandler(t, events, true),
		falcon.WithMaxFetchRecords(100), falcon.WithMaxRecords(10))

	res := gt.R1(ts.Run(context.Background(), "falcon_search_events", map[string]any{"query_string": "aid=abc"})).NoError(t)
	gt.Value(t, res["total"]).Equal(2)
	gt.Value(t, res["done"]).Equal(true)
	gt.Value(t, res["truncated"]).Equal(false)
	gt.Value(t, res["has_more"]).Equal(false)
	gt.Value(t, res["metadata"]).NotEqual(nil)
}

func TestSearchEventsRequiresQuery(t *testing.T) {
	ts, _ := newToolSet(t, func(w http.ResponseWriter, r *http.Request) {})
	_, err := ts.Run(context.Background(), "falcon_search_events", map[string]any{})
	gt.Error(t, err).Contains("query_string is required")
}
