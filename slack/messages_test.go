package slack_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gollem-dev/tools/slack"
	"github.com/m-mizutani/gt"
)

// newMessagesServer returns an httptest server that answers conversations.replies
// and chat.getPermalink. The replies handler returns a thread keyed by ts.
func newMessagesServer(t *testing.T, threads map[string][]map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/conversations.replies":
			ts := r.URL.Query().Get("ts")
			msgs, ok := threads[ts]
			if !ok {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "thread_not_found"})
				return
			}
			// Honor limit by truncating.
			limit := len(msgs)
			if v := r.URL.Query().Get("limit"); v != "" {
				if n, err := strconv.Atoi(v); err == nil && n < limit {
					limit = n
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": msgs[:limit]})
		case "/chat.getPermalink":
			ch := r.URL.Query().Get("channel")
			ts := r.URL.Query().Get("message_ts")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":        true,
				"permalink": "https://example.slack.com/archives/" + ch + "/p" + ts,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGetMessagesHappyPath(t *testing.T) {
	threads := map[string][]map[string]any{
		"1700000000.000100": {
			{"user": "U1", "username": "alice", "text": "root", "ts": "1700000000.000100", "thread_ts": "1700000000.000100"},
			{"user": "U2", "username": "bob", "text": "reply", "ts": "1700000000.000200", "thread_ts": "1700000000.000100"},
		},
		"1700000000.000300": {
			{"user": "U3", "text": "lonely", "ts": "1700000000.000300"},
		},
	}
	srv := newMessagesServer(t, threads)

	ts := gt.R1(slack.New("xoxp-test", slack.WithBaseURL(srv.URL), slack.WithHTTPClient(srv.Client()))).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "slack_get_messages", map[string]any{
		"targets": []any{
			map[string]any{"channel_id": "C1", "ts": "1700000000.000100"},
			map[string]any{"channel_id": "C1", "ts": "1700000000.000300"},
		},
	})).NoError(t)

	results := result["results"].([]any)
	gt.Array(t, results).Length(2)

	first := results[0].(map[string]any)
	gt.Value(t, first["channel_id"]).Equal("C1")
	gt.Value(t, first["permalink"]).Equal("https://example.slack.com/archives/C1/p1700000000.000100")
	firstMsgs := first["messages"].([]any)
	gt.Array(t, firstMsgs).Length(2)
	m0 := firstMsgs[0].(map[string]any)
	gt.Value(t, m0["user_id"]).Equal("U1")
	gt.Value(t, m0["username"]).Equal("alice")
	gt.Value(t, m0["text"]).Equal("root")

	second := results[1].(map[string]any)
	secondMsgs := second["messages"].([]any)
	gt.Array(t, secondMsgs).Length(1)
}

func TestGetMessagesPartialFailure(t *testing.T) {
	threads := map[string][]map[string]any{
		"1700000000.000100": {
			{"user": "U1", "text": "ok", "ts": "1700000000.000100"},
		},
		// "bad" ts intentionally absent → thread_not_found.
	}
	srv := newMessagesServer(t, threads)

	ts := gt.R1(slack.New("xoxp-test", slack.WithBaseURL(srv.URL), slack.WithHTTPClient(srv.Client()))).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "slack_get_messages", map[string]any{
		"targets": []any{
			map[string]any{"channel_id": "C1", "ts": "1700000000.000100"},
			map[string]any{"channel_id": "C1", "ts": "bad"},
		},
	})).NoError(t)

	results := result["results"].([]any)
	gt.Array(t, results).Length(2)

	good := results[0].(map[string]any)
	_, hasErr := good["error"]
	gt.Value(t, hasErr).Equal(false)
	gt.Array(t, good["messages"].([]any)).Length(1)

	bad := results[1].(map[string]any)
	gt.Value(t, bad["error"]).NotEqual(nil)
	gt.String(t, bad["error"].(string)).Contains("conversations.replies")
}

func TestGetMessagesAllFailed(t *testing.T) {
	srv := newMessagesServer(t, map[string][]map[string]any{})

	ts := gt.R1(slack.New("xoxp-test", slack.WithBaseURL(srv.URL), slack.WithHTTPClient(srv.Client()))).NoError(t)

	_, err := ts.Run(context.Background(), "slack_get_messages", map[string]any{
		"targets": []any{
			map[string]any{"channel_id": "C1", "ts": "x"},
			map[string]any{"channel_id": "C1", "ts": "y"},
		},
	})
	gt.Error(t, err).Contains("all Slack message targets failed")
}

func TestGetMessagesIncludeThreadFalse(t *testing.T) {
	var capturedLimit atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/conversations.replies":
			if v := r.URL.Query().Get("limit"); v != "" {
				if n, err := strconv.Atoi(v); err == nil {
					capturedLimit.Store(int64(n))
				}
			}
			// Always return only one message regardless.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{"user": "U1", "text": "root", "ts": "1.1"},
				},
			})
		case "/chat.getPermalink":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permalink": "https://x"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	ts := gt.R1(slack.New("xoxp-test", slack.WithBaseURL(srv.URL), slack.WithHTTPClient(srv.Client()))).NoError(t)

	_ = gt.R1(ts.Run(context.Background(), "slack_get_messages", map[string]any{
		"targets":        []any{map[string]any{"channel_id": "C1", "ts": "1.1"}},
		"include_thread": false,
	})).NoError(t)

	gt.Value(t, capturedLimit.Load()).Equal(int64(1))
}

func TestGetMessagesThreadLimitCap(t *testing.T) {
	var capturedLimit atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/conversations.replies":
			if v := r.URL.Query().Get("limit"); v != "" {
				if n, err := strconv.Atoi(v); err == nil {
					capturedLimit.Store(int64(n))
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": []map[string]any{{"user": "U1", "ts": "1.1"}}})
		case "/chat.getPermalink":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permalink": "https://x"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	ts := gt.R1(slack.New("xoxp-test", slack.WithBaseURL(srv.URL), slack.WithHTTPClient(srv.Client()))).NoError(t)

	_ = gt.R1(ts.Run(context.Background(), "slack_get_messages", map[string]any{
		"targets":      []any{map[string]any{"channel_id": "C1", "ts": "1.1"}},
		"thread_limit": float64(9999),
	})).NoError(t)

	// Capped at maxThreadLimit (200).
	gt.Value(t, capturedLimit.Load()).Equal(int64(200))
}

func TestGetMessagesValidation(t *testing.T) {
	ts := gt.R1(slack.New("xoxp-test")).NoError(t)

	t.Run("missing targets", func(t *testing.T) {
		_, err := ts.Run(context.Background(), "slack_get_messages", map[string]any{})
		gt.Error(t, err).Contains("targets is required")
	})

	t.Run("empty targets", func(t *testing.T) {
		_, err := ts.Run(context.Background(), "slack_get_messages", map[string]any{"targets": []any{}})
		gt.Error(t, err).Contains("targets is required")
	})

	t.Run("missing channel_id", func(t *testing.T) {
		_, err := ts.Run(context.Background(), "slack_get_messages", map[string]any{
			"targets": []any{map[string]any{"ts": "1.1"}},
		})
		gt.Error(t, err).Contains("channel_id and ts")
	})

	t.Run("too many targets", func(t *testing.T) {
		targets := make([]any, 11)
		for i := range targets {
			targets[i] = map[string]any{"channel_id": "C1", "ts": "1.1"}
		}
		_, err := ts.Run(context.Background(), "slack_get_messages", map[string]any{"targets": targets})
		gt.Error(t, err).Contains("too many targets")
	})
}

func TestGetMessagesRateLimitRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/conversations.replies":
			if calls.Add(1) == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte("rate limited"))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": []map[string]any{{"user": "U1", "ts": "1.1"}}})
		case "/chat.getPermalink":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permalink": "https://x"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	ts := gt.R1(slack.New("xoxp-test",
		slack.WithBaseURL(srv.URL),
		slack.WithHTTPClient(srv.Client()),
		slack.WithRetryWaitForTest(time.Millisecond),
	)).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "slack_get_messages", map[string]any{
		"targets": []any{map[string]any{"channel_id": "C1", "ts": "1.1"}},
	})).NoError(t)

	results := result["results"].([]any)
	first := results[0].(map[string]any)
	_, hasErr := first["error"]
	gt.Value(t, hasErr).Equal(false)
	gt.Number(t, calls.Load()).Equal(int32(2))
}
