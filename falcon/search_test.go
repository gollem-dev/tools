package falcon_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/gollem-dev/tools/falcon"
	"github.com/m-mizutani/gt"
)

// TestSearchIncidentsPaginatesInMemory verifies the offset-based search fetches
// up to maxFetchRecords, returns one page, caches the rest, and serves the
// remaining pages from memory without hitting the API again.
func TestSearchIncidentsPaginatesInMemory(t *testing.T) {
	const total = 10
	handler := func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		offset, _ := strconv.Atoi(q.Get("offset"))
		var res []any
		for i := offset; i < offset+limit && i < total; i++ {
			res = append(res, fmt.Sprintf("inc-%d", i))
		}
		writeJSON(t, w, map[string]any{
			"resources": res,
			"meta":      map[string]any{"pagination": map[string]any{"total": total, "offset": offset, "limit": limit}},
		})
	}
	ts, calls := newToolSet(t, handler, falcon.WithMaxFetchRecords(5), falcon.WithMaxRecords(2))
	ctx := context.Background()

	// First call: fetch 5 (capped), return a page of 2, cache 3.
	res := gt.R1(ts.Run(ctx, "falcon_search_incidents", map[string]any{"filter": "status:'30'"})).NoError(t)
	gt.Array(t, res["records"].([]any)).Equal([]any{"inc-0", "inc-1"})
	gt.Value(t, res["count"]).Equal(2)
	gt.Value(t, res["total"]).Equal(5)
	gt.Value(t, res["has_more"]).Equal(true)
	gt.Value(t, res["truncated"]).Equal(true)
	gt.Value(t, res["total_available"]).Equal(total)
	token := gt.Cast[string](t, res["page_token"])
	gt.String(t, token).NotEqual("")
	gt.Number(t, *calls).Equal(1)

	// Second page from memory: no new API call.
	res2 := gt.R1(ts.Run(ctx, "falcon_search_incidents", map[string]any{"page_token": token})).NoError(t)
	gt.Array(t, res2["records"].([]any)).Equal([]any{"inc-2", "inc-3"})
	gt.Value(t, res2["has_more"]).Equal(true)
	gt.Number(t, *calls).Equal(1)

	// Final page exhausts the entry.
	res3 := gt.R1(ts.Run(ctx, "falcon_search_incidents", map[string]any{"page_token": token})).NoError(t)
	gt.Array(t, res3["records"].([]any)).Equal([]any{"inc-4"})
	gt.Value(t, res3["has_more"]).Equal(false)
	gt.Number(t, *calls).Equal(1)

	// The token is now invalid.
	_, err := ts.Run(ctx, "falcon_search_incidents", map[string]any{"page_token": token})
	gt.Error(t, err).Contains("page_token")
}

// TestSearchAlertsFollowsAfterCursor verifies the alerts search walks the
// "after" cursor across multiple upstream requests up to maxFetchRecords.
func TestSearchAlertsFollowsAfterCursor(t *testing.T) {
	const (
		total      = 10
		perRequest = 3
	)
	handler := func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		start := 0
		if a, ok := body["after"].(string); ok && a != "" {
			start, _ = strconv.Atoi(a)
		}
		var res []any
		for i := start; i < start+perRequest && i < total; i++ {
			res = append(res, map[string]any{"composite_id": fmt.Sprintf("alert-%d", i)})
		}
		next := ""
		if start+len(res) < total {
			next = strconv.Itoa(start + len(res))
		}
		writeJSON(t, w, map[string]any{
			"resources": res,
			"meta":      map[string]any{"pagination": map[string]any{"total": total, "after": next}},
		})
	}
	ts, calls := newToolSet(t, handler, falcon.WithMaxFetchRecords(6), falcon.WithMaxRecords(4))
	ctx := context.Background()

	res := gt.R1(ts.Run(ctx, "falcon_search_alerts", map[string]any{"filter": "severity:>50"})).NoError(t)
	gt.Value(t, res["total"]).Equal(6)        // fetched 2 pages of 3 = 6 (cap)
	gt.Value(t, res["count"]).Equal(4)        // first page = maxRecords
	gt.Value(t, res["has_more"]).Equal(true)  // 2 remain in memory
	gt.Value(t, res["truncated"]).Equal(true) // 10 > 6 upstream
	gt.Value(t, res["total_available"]).Equal(10)
	gt.Number(t, *calls).Equal(2) // two upstream requests to reach the cap
}

// TestSearchDevicesFollowsScrollOffset verifies the devices-scroll string-token
// pagination is followed internally.
func TestSearchDevicesFollowsScrollOffset(t *testing.T) {
	const (
		total      = 10
		perRequest = 3
	)
	handler := func(w http.ResponseWriter, r *http.Request) {
		start := 0
		if o := r.URL.Query().Get("offset"); o != "" {
			start, _ = strconv.Atoi(o)
		}
		var res []any
		for i := start; i < start+perRequest && i < total; i++ {
			res = append(res, fmt.Sprintf("dev-%d", i))
		}
		next := ""
		if start+len(res) < total {
			next = strconv.Itoa(start + len(res))
		}
		writeJSON(t, w, map[string]any{
			"resources": res,
			"meta":      map[string]any{"pagination": map[string]any{"total": total, "offset": next}},
		})
	}
	ts, calls := newToolSet(t, handler, falcon.WithMaxFetchRecords(6), falcon.WithMaxRecords(10))
	ctx := context.Background()

	res := gt.R1(ts.Run(ctx, "falcon_search_devices", map[string]any{"filter": "platform_name:'Windows'"})).NoError(t)
	gt.Value(t, res["total"]).Equal(6)
	gt.Value(t, res["count"]).Equal(6) // maxRecords(10) >= held(6) => single page
	gt.Value(t, res["has_more"]).Equal(false)
	gt.Value(t, res["truncated"]).Equal(true)
	gt.Number(t, *calls).Equal(2)
}

// TestGetDevicesClampsIDs verifies the get_* tools clamp the input ID list to
// maxRecords and note the dropped IDs.
func TestGetDevicesClampsIDs(t *testing.T) {
	var gotIDs []any
	handler := func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		gotIDs, _ = body["ids"].([]any)
		writeJSON(t, w, map[string]any{"resources": gotIDs})
	}
	ts, _ := newToolSet(t, handler, falcon.WithMaxRecords(2))
	ctx := context.Background()

	res := gt.R1(ts.Run(ctx, "falcon_get_devices", map[string]any{"ids": "a,b,c,d"})).NoError(t)
	gt.Array(t, gotIDs).Length(2) // only the first 2 IDs are sent upstream
	gt.Value(t, res["note"]).NotEqual(nil)
	gt.String(t, gt.Cast[string](t, res["note"])).Contains("dropped")
}

func TestGetDevicesRequiresIDs(t *testing.T) {
	ts, _ := newToolSet(t, func(w http.ResponseWriter, r *http.Request) {})
	_, err := ts.Run(context.Background(), "falcon_get_devices", map[string]any{})
	gt.Error(t, err).Contains("required")
}

// TestGetCrowdScoresTruncates verifies CrowdScores are capped at maxRecords.
func TestGetCrowdScoresTruncates(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		res := make([]any, 0, 5)
		for i := range 5 {
			res = append(res, map[string]any{"id": fmt.Sprintf("score-%d", i)})
		}
		writeJSON(t, w, map[string]any{"resources": res})
	}
	ts, _ := newToolSet(t, handler, falcon.WithMaxRecords(2))
	res := gt.R1(ts.Run(context.Background(), "falcon_get_crowdscores", map[string]any{})).NoError(t)
	gt.Array(t, res["records"].([]any)).Length(2)
	gt.Value(t, res["truncated"]).Equal(true)
}

// TestDoRequestRetriesOn401 verifies a 401 clears the token and the request is
// retried once.
func TestDoRequestRetriesOn401(t *testing.T) {
	attempt := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if attempt == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"errors":[{"message":"expired"}]}`))
			return
		}
		writeJSON(t, w, map[string]any{
			"resources": []any{"inc-0"},
			"meta":      map[string]any{"pagination": map[string]any{"total": 1}},
		})
	}
	ts, calls := newToolSet(t, handler, falcon.WithMaxFetchRecords(5), falcon.WithMaxRecords(2))
	res := gt.R1(ts.Run(context.Background(), "falcon_search_incidents", map[string]any{})).NoError(t)
	gt.Array(t, res["records"].([]any)).Equal([]any{"inc-0"})
	gt.Number(t, *calls).Equal(2) // 401 then retry
}
