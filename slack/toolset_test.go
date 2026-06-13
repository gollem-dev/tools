package slack_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gollem-dev/tools/slack"
	"github.com/m-mizutani/gt"
)

// TestNewRequiresUserToken verifies that New returns an error when an empty token is provided.
func TestNewRequiresUserToken(t *testing.T) {
	_, err := slack.New("")
	gt.Error(t, err).Contains("user token is required")
}

// TestSpecs verifies the tool specifications returned by Specs.
func TestSpecs(t *testing.T) {
	ts := gt.R1(slack.New("xoxp-dummy")).NoError(t)

	specs := gt.R1(ts.Specs(context.Background())).NoError(t)
	gt.Array(t, specs).Length(1)

	spec := specs[0]
	gt.String(t, spec.Name).Equal("slack_message_search")
	gt.Map(t, spec.Parameters).HasKey("query")
	gt.Map(t, spec.Parameters).HasKey("sort")
	gt.Map(t, spec.Parameters).HasKey("sort_dir")
	gt.Map(t, spec.Parameters).HasKey("count")
	gt.Map(t, spec.Parameters).HasKey("page")
	gt.Map(t, spec.Parameters).HasKey("highlight")
}

// TestRunInvalidName verifies that Run returns an error for unknown tool names.
func TestRunInvalidName(t *testing.T) {
	ts := gt.R1(slack.New("xoxp-dummy")).NoError(t)
	_, err := ts.Run(context.Background(), "slack_unknown", map[string]any{"query": "test"})
	gt.Error(t, err).Contains("invalid function name")
}

// TestRunMissingQuery verifies that Run returns an error when query is absent.
func TestRunMissingQuery(t *testing.T) {
	ts := gt.R1(slack.New("xoxp-dummy")).NoError(t)
	_, err := ts.Run(context.Background(), "slack_message_search", map[string]any{})
	gt.Error(t, err).Contains("query is required")
}

// TestRunHappyPath verifies that Run issues a GET with the correct Bearer
// header and query parameter, and returns the mapped result.
func TestRunHappyPath(t *testing.T) {
	var gotAuthHeader, gotQuery string

	responsePayload := map[string]any{
		"ok":    true,
		"query": "security alert",
		"messages": map[string]any{
			"total":  float64(1),
			"paging": map[string]any{"count": 1, "total": 1, "page": 1, "pages": 1},
			"matches": []any{
				map[string]any{
					"channel":   map[string]any{"id": "C123", "name": "general"},
					"user":      "U456",
					"username":  "alice",
					"text":      "security alert triggered",
					"ts":        "1700000000.000000",
					"permalink": "https://example.slack.com/p/123",
				},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search.messages":
			gotAuthHeader = r.Header.Get("Authorization")
			gotQuery = r.URL.Query().Get("query")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(responsePayload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ts := gt.R1(slack.New(
		"xoxp-testtoken",
		slack.WithBaseURL(srv.URL),
		slack.WithHTTPClient(srv.Client()),
	)).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "slack_message_search", map[string]any{
		"query": "security alert",
	})).NoError(t)

	gt.String(t, gotAuthHeader).Equal("Bearer xoxp-testtoken")
	gt.String(t, gotQuery).Equal("security alert")

	gt.Map(t, result).HasKey("total")
	gt.Map(t, result).HasKey("messages")

	msgs, ok := result["messages"].([]any)
	gt.Bool(t, ok).True()
	gt.Array(t, msgs).Length(1)

	first := gt.Cast[map[string]any](t, msgs[0])
	gt.Map(t, first).HasKeyValue("channel", "C123")
	gt.Map(t, first).HasKeyValue("channel_name", "general")
	gt.Map(t, first).HasKeyValue("user_name", "alice")
}

// TestRunSlackAPIError verifies that an ok:false response is propagated as an error.
func TestRunSlackAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "invalid_auth",
		})
	}))
	defer srv.Close()

	ts := gt.R1(slack.New(
		"xoxp-bad",
		slack.WithBaseURL(srv.URL),
		slack.WithHTTPClient(srv.Client()),
	)).NoError(t)

	_, err := ts.Run(context.Background(), "slack_message_search", map[string]any{
		"query": "test",
	})
	gt.Error(t, err).Contains("Slack API error")
}

// TestRunRateLimitRetry verifies that a 429 response is retried and the
// subsequent success is returned (regression guard for the rate-limit retry).
func TestRunRateLimitRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("rate limited"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"messages": map[string]any{
				"total": 1,
				"matches": []map[string]any{
					{"text": "hi", "channel": map[string]any{"id": "C1", "name": "general"}},
				},
			},
		})
	}))
	defer srv.Close()

	ts := gt.R1(slack.New(
		"xoxp-k",
		slack.WithBaseURL(srv.URL),
		slack.WithHTTPClient(srv.Client()),
		slack.WithRetryWaitForTest(time.Millisecond),
	)).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "slack_message_search", map[string]any{
		"query": "x",
	})).NoError(t)

	gt.Map(t, result).HasKey("messages")
	gt.Number(t, calls.Load()).Equal(int32(2))
}

// TestRunRateLimitExhausted verifies that persistent 429s eventually fail after
// the retry budget is exhausted, having attempted maxSearchRetries times.
func TestRunRateLimitExhausted(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer srv.Close()

	ts := gt.R1(slack.New(
		"xoxp-k",
		slack.WithBaseURL(srv.URL),
		slack.WithHTTPClient(srv.Client()),
		slack.WithRetryWaitForTest(time.Millisecond),
	)).NoError(t)

	_, err := ts.Run(context.Background(), "slack_message_search", map[string]any{"query": "x"})
	gt.Error(t, err).Contains("after retries")
	gt.Number(t, calls.Load()).Equal(int32(3))
}

// TestPing verifies that Ping calls auth.test and succeeds on ok:true.
func TestPing(t *testing.T) {
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	ts := gt.R1(slack.New(
		"xoxp-k",
		slack.WithBaseURL(srv.URL),
		slack.WithHTTPClient(srv.Client()),
	)).NoError(t)

	gt.NoError(t, ts.Ping(context.Background()))
	gt.String(t, gotPath).Equal("/auth.test")
}

// TestPingAuthFailure verifies that Ping returns an error when ok is false.
func TestPingAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid_auth"})
	}))
	defer srv.Close()

	ts := gt.R1(slack.New(
		"xoxp-bad",
		slack.WithBaseURL(srv.URL),
		slack.WithHTTPClient(srv.Client()),
	)).NoError(t)

	err := ts.Ping(context.Background())
	gt.Error(t, err).Contains("auth.test failed")
}

// TestLive hits the real Slack API. It runs only when TEST_SLACK_USER_TOKEN and
// TEST_SLACK_QUERY are set. The token must be a user token (xoxp-…) with the
// search:read scope.
func TestLive(t *testing.T) {
	token, ok := os.LookupEnv("TEST_SLACK_USER_TOKEN")
	if !ok {
		t.Skip("TEST_SLACK_USER_TOKEN is not set")
	}
	query, ok := os.LookupEnv("TEST_SLACK_QUERY")
	if !ok {
		t.Skip("TEST_SLACK_QUERY is not set")
	}

	ts := gt.R1(slack.New(token)).NoError(t)

	gt.NoError(t, ts.Ping(context.Background())).Required()

	result := gt.R1(ts.Run(context.Background(), "slack_message_search", map[string]any{
		"query": query,
	})).NoError(t)

	gt.Map(t, result).HasKey("total")
	gt.Map(t, result).HasKey("messages")

	// Verify the payload round-trips to JSON without error.
	_ = gt.R1(json.Marshal(result)).NoError(t)
}
