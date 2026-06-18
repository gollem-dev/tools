package falcon_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gollem-dev/tools/falcon"
	"github.com/m-mizutani/gt"
)

// writeJSON encodes v as a JSON response body.
func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("failed to encode response: %v", err)
	}
}

// tokenResp is the canned OAuth2 token reply used by the mock server.
func tokenResp() map[string]any {
	return map[string]any{"access_token": "test-token", "expires_in": 1800, "token_type": "bearer"}
}

// newToolSet wires a ToolSet to a mock server. handler serves every non-token
// request; the returned counter reports how many such API requests were made
// (the /oauth2/token endpoint is excluded), letting tests assert that cached
// pages do not hit the backend.
func newToolSet(t *testing.T, handler http.HandlerFunc, opts ...falcon.Option) (*falcon.ToolSet, *int) {
	t.Helper()
	apiCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/token" {
			writeJSON(t, w, tokenResp())
			return
		}
		apiCalls++
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	base := []falcon.Option{
		falcon.WithBaseURL(srv.URL),
		falcon.WithHTTPClient(srv.Client()),
	}
	ts := gt.R1(falcon.New("client-id", "client-secret", append(base, opts...)...)).NoError(t)
	return ts, &apiCalls
}

func TestNewRequiresCredentials(t *testing.T) {
	_, err := falcon.New("", "secret")
	gt.Error(t, err).Contains("client ID")

	_, err = falcon.New("id", "")
	gt.Error(t, err).Contains("client secret")

	gt.R1(falcon.New("id", "secret")).NoError(t)
}

func TestSpecs(t *testing.T) {
	ts := gt.R1(falcon.New("id", "secret")).NoError(t)
	specs := gt.R1(ts.Specs(context.Background())).NoError(t)
	gt.Array(t, specs).Length(10)

	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
	}
	gt.Array(t, names).
		Has("falcon_search_incidents").Has("falcon_get_incidents").
		Has("falcon_search_alerts").Has("falcon_get_alerts").
		Has("falcon_search_behaviors").Has("falcon_get_behaviors").
		Has("falcon_search_devices").Has("falcon_get_devices").
		Has("falcon_get_crowdscores").Has("falcon_search_events")

	// Every search tool exposes a page_token parameter for in-memory paging.
	searchTools := map[string]bool{
		"falcon_search_incidents": true,
		"falcon_search_alerts":    true,
		"falcon_search_behaviors": true,
		"falcon_search_devices":   true,
		"falcon_search_events":    true,
	}
	for _, s := range specs {
		if searchTools[s.Name] {
			gt.Map(t, s.Parameters).HasKey("page_token")
		}
	}
}

func TestRunUnknownTool(t *testing.T) {
	ts := gt.R1(falcon.New("id", "secret")).NoError(t)
	_, err := ts.Run(context.Background(), "falcon_unknown", map[string]any{})
	gt.Error(t, err).Contains("unknown tool name")
}

func TestPing(t *testing.T) {
	ts, calls := newToolSet(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("Ping must not hit the API, but %s was called", r.URL.Path)
	})
	gt.NoError(t, ts.Ping(context.Background()))
	gt.Number(t, *calls).Equal(0) // only the token endpoint is used
}

// liveToolSet builds a ToolSet against the real CrowdStrike Falcon API from the
// TEST_FALCON_* environment variables, skipping the test when they are unset.
// The small maxRecords/maxFetchRecords keep the live calls light while still
// exercising the in-memory pagination path. TEST_FALCON_BASE_URL optionally
// selects a cloud region.
func liveToolSet(t *testing.T) *falcon.ToolSet {
	t.Helper()
	clientID, ok := os.LookupEnv("TEST_FALCON_CLIENT_ID")
	if !ok {
		t.Skip("TEST_FALCON_CLIENT_ID not set")
	}
	clientSecret, ok := os.LookupEnv("TEST_FALCON_CLIENT_SECRET")
	if !ok {
		t.Skip("TEST_FALCON_CLIENT_SECRET not set")
	}

	opts := []falcon.Option{
		falcon.WithMaxRecords(5),
		falcon.WithMaxFetchRecords(50),
	}
	if baseURL, ok := os.LookupEnv("TEST_FALCON_BASE_URL"); ok {
		opts = append(opts, falcon.WithBaseURL(baseURL))
	}
	return gt.R1(falcon.New(clientID, clientSecret, opts...)).NoError(t)
}

// assertSearchEnvelope checks the uniform shape returned by the search tools.
func assertSearchEnvelope(t *testing.T, res map[string]any) {
	t.Helper()
	gt.Map(t, res).HasKey("records").HasKey("count").HasKey("has_more").HasKey("truncated")
	gt.Array(t, res["records"].([]any)).Length(res["count"].(int))
}

// TestLivePing verifies that the configured credentials acquire an OAuth2 token.
func TestLivePing(t *testing.T) {
	ts := liveToolSet(t)
	gt.NoError(t, ts.Ping(context.Background()))
}

// TestLiveSearchTools exercises the search tools whose scopes (Hosts, Alerts)
// the device-pagination flow already proves are present. devices uses scroll
// pagination and alerts uses the "after" cursor, so this covers both live.
// Results may legitimately be empty (a quiet tenant); the test only asserts the
// call succeeds and returns the expected envelope.
func TestLiveSearchTools(t *testing.T) {
	ts := liveToolSet(t)
	ctx := context.Background()

	for _, name := range []string{"falcon_search_devices", "falcon_search_alerts"} {
		t.Run(name, func(t *testing.T) {
			res := gt.R1(ts.Run(ctx, name, map[string]any{})).NoError(t)
			assertSearchEnvelope(t, res)
		})
	}
}

// TestLiveIncidentScopeTools exercises the tools backed by the Incidents API
// (incidents, behaviors, CrowdScores). Even with the Incidents:Read scope on the
// token, the CrowdStrike Incidents API returns HTTP 500 on tenants where the
// underlying incidents data product is not enabled, so this runs only when
// TEST_FALCON_INCIDENTS_SCOPE is set to confirm the tenant's API actually works.
func TestLiveIncidentScopeTools(t *testing.T) {
	ts := liveToolSet(t)
	if _, ok := os.LookupEnv("TEST_FALCON_INCIDENTS_SCOPE"); !ok {
		t.Skip("TEST_FALCON_INCIDENTS_SCOPE not set")
	}
	ctx := context.Background()

	for _, name := range []string{"falcon_search_incidents", "falcon_search_behaviors"} {
		t.Run(name, func(t *testing.T) {
			res := gt.R1(ts.Run(ctx, name, map[string]any{})).NoError(t)
			assertSearchEnvelope(t, res)
		})
	}

	t.Run("falcon_get_crowdscores", func(t *testing.T) {
		res := gt.R1(ts.Run(ctx, "falcon_get_crowdscores", map[string]any{})).NoError(t)
		gt.Map(t, res).HasKey("records").HasKey("count")
	})
}

// TestLiveDevicePaginationRoundTrip verifies the full in-memory pagination flow
// against the live API: search devices, then walk subsequent pages via
// page_token, and confirm get_devices fetches details for the returned IDs.
func TestLiveDevicePaginationRoundTrip(t *testing.T) {
	ts := liveToolSet(t)
	ctx := context.Background()

	first := gt.R1(ts.Run(ctx, "falcon_search_devices", map[string]any{})).NoError(t)
	assertSearchEnvelope(t, first)

	firstIDs := first["records"].([]any)
	if len(firstIDs) == 0 {
		t.Skip("no devices in tenant; nothing to paginate")
	}

	// Walk every remaining page through the same page_token until exhausted,
	// bounding the loop so a misbehaving cursor cannot spin forever.
	pages, totalRecords := 1, len(firstIDs)
	cur := first
	for hasMore, _ := cur["has_more"].(bool); hasMore; hasMore, _ = cur["has_more"].(bool) {
		token := gt.Cast[string](t, cur["page_token"])
		cur = gt.R1(ts.Run(ctx, "falcon_search_devices", map[string]any{"page_token": token})).NoError(t)
		gt.Map(t, cur).HasKey("records").HasKey("has_more")
		totalRecords += len(cur["records"].([]any))
		pages++
		if pages > 100 {
			t.Fatal("pagination did not terminate within 100 pages")
		}
	}
	t.Logf("paginated %d device records across %d page(s)", totalRecords, pages)

	// The first page of IDs should resolve to full device details.
	ids := make([]string, 0, len(firstIDs))
	for _, v := range firstIDs {
		if s, ok := v.(string); ok {
			ids = append(ids, s)
		}
	}
	details := gt.R1(ts.Run(ctx, "falcon_get_devices", map[string]any{"ids": strings.Join(ids, ",")})).NoError(t)
	gt.Map(t, details).HasKey("resources")
}

// TestLiveSearchEvents exercises the Next-Gen SIEM event search. It runs only
// when TEST_FALCON_EVENTS_QUERY is set, because the NGSIEM scope is optional and
// a CQL query must be supplied for the tenant's data.
func TestLiveSearchEvents(t *testing.T) {
	ts := liveToolSet(t)
	query, ok := os.LookupEnv("TEST_FALCON_EVENTS_QUERY")
	if !ok {
		t.Skip("TEST_FALCON_EVENTS_QUERY not set")
	}

	res := gt.R1(ts.Run(context.Background(), "falcon_search_events", map[string]any{
		"query_string": query,
		"start":        "1h",
	})).NoError(t)
	gt.Map(t, res).HasKey("records").HasKey("done").HasKey("repository")
}
