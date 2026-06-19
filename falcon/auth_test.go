package falcon_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gollem-dev/tools/falcon"
	"github.com/m-mizutani/gt"
)

// TestPingTokenFailure verifies that a failed token request surfaces as an error.
func TestPingTokenFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"errors":["bad credentials"]}`))
	}))
	defer srv.Close()

	ts := gt.R1(falcon.New("id", "secret",
		falcon.WithBaseURL(srv.URL),
		falcon.WithHTTPClient(srv.Client()),
	)).NoError(t)

	err := ts.Ping(context.Background())
	gt.Error(t, err).Contains("ping failed")
}

// TestTokenIsCachedAcrossRequests verifies the OAuth2 token is fetched once and
// reused for subsequent API calls within its validity window.
func TestTokenIsCachedAcrossRequests(t *testing.T) {
	var tokenCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/token" {
			tokenCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":1800,"token_type":"bearer"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resources":[],"meta":{"pagination":{"total":0}}}`))
	}))
	defer srv.Close()

	ts := gt.R1(falcon.New("id", "secret",
		falcon.WithBaseURL(srv.URL),
		falcon.WithHTTPClient(srv.Client()),
		falcon.WithMaxFetchRecords(5),
	)).NoError(t)
	ctx := context.Background()

	gt.R1(ts.Run(ctx, "falcon_search_incidents", map[string]any{})).NoError(t)
	gt.R1(ts.Run(ctx, "falcon_search_behaviors", map[string]any{})).NoError(t)

	gt.Number(t, int(tokenCalls.Load())).Equal(1) // token reused, not re-fetched
}
