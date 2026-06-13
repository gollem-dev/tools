package urlscan_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gollem-dev/tools/urlscan"
	"github.com/m-mizutani/gt"
)

func TestNewRequiresAPIKey(t *testing.T) {
	_, err := urlscan.New()
	gt.Error(t, err).Contains("API key")
}

func TestNewWithAPIKey(t *testing.T) {
	ts := gt.R1(urlscan.New(urlscan.WithAPIKey("test-key"))).NoError(t)
	gt.Value(t, ts).NotNil()
}

func TestSpecs(t *testing.T) {
	ts := gt.R1(urlscan.New(urlscan.WithAPIKey("dummy"))).NoError(t)

	specs := gt.R1(ts.Specs(context.Background())).NoError(t)
	gt.Array(t, specs).Length(1)
	gt.Value(t, specs[0].Name).Equal("urlscan_scan")
	gt.Map(t, specs[0].Parameters).HasKey("url")
}

func TestRunInvalidName(t *testing.T) {
	ts := gt.R1(urlscan.New(urlscan.WithAPIKey("dummy"))).NoError(t)
	_, err := ts.Run(context.Background(), "urlscan_unknown", map[string]any{"url": "https://example.com"})
	gt.Error(t, err).Contains("invalid function name")
}

func TestRunMissingURL(t *testing.T) {
	ts := gt.R1(urlscan.New(urlscan.WithAPIKey("dummy"))).NoError(t)
	_, err := ts.Run(context.Background(), "urlscan_scan", map[string]any{})
	gt.Error(t, err).Contains("url parameter is required")
}

// TestRunMock uses httptest to simulate the full submit→poll lifecycle:
// first poll returns 404 (not ready), second returns 200 with a result.
func TestRunMock(t *testing.T) {
	var pollCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/scan/":
			// Validate the API key header.
			gt.String(t, r.Header.Get("API-Key")).Equal("test-key")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"uuid":   "test-uuid-123",
				"result": r.Host + "/result/test-uuid-123/",
			})

		case r.Method == http.MethodGet && r.URL.Path == "/result/test-uuid-123/":
			n := pollCount.Add(1)
			if n == 1 {
				// First poll: not ready yet.
				w.WriteHeader(http.StatusNotFound)
				return
			}
			// Second poll: ready.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"requests": []any{},
				},
				"page": map[string]any{
					"url": "https://example.com",
				},
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	ts := gt.R1(urlscan.New(
		urlscan.WithAPIKey("test-key"),
		urlscan.WithBaseURL(srv.URL),
		urlscan.WithHTTPClient(srv.Client()),
		urlscan.WithBackoff(1*time.Millisecond),
		urlscan.WithTimeout(5*time.Second),
	)).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "urlscan_scan", map[string]any{"url": "https://example.com"})).NoError(t)
	gt.Map(t, result).HasKey("page")

	// Two polls were made: one 404, one 200.
	gt.Number(t, int(pollCount.Load())).Equal(2)
}

func TestRunHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Unauthorized"}`))
	}))
	defer srv.Close()

	ts := gt.R1(urlscan.New(
		urlscan.WithAPIKey("bad-key"),
		urlscan.WithBaseURL(srv.URL),
		urlscan.WithHTTPClient(srv.Client()),
	)).NoError(t)

	_, err := ts.Run(context.Background(), "urlscan_scan", map[string]any{"url": "https://example.com"})
	gt.Error(t, err).Contains("failed to submit urlscan request")
}

func TestRunTimeout(t *testing.T) {
	// The server always returns 404 so the poll loop times out.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"uuid": "slow-uuid"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	ts := gt.R1(urlscan.New(
		urlscan.WithAPIKey("key"),
		urlscan.WithBaseURL(srv.URL),
		urlscan.WithHTTPClient(srv.Client()),
		urlscan.WithBackoff(1*time.Millisecond),
		urlscan.WithTimeout(5*time.Millisecond),
	)).NoError(t)

	_, err := ts.Run(context.Background(), "urlscan_scan", map[string]any{"url": "https://example.com"})
	gt.Error(t, err).Contains("timed out")
}

func TestRunContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"uuid": "ctx-uuid"})
			return
		}
		// Simulate always-pending result.
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())

	ts := gt.R1(urlscan.New(
		urlscan.WithAPIKey("key"),
		urlscan.WithBaseURL(srv.URL),
		urlscan.WithHTTPClient(srv.Client()),
		urlscan.WithBackoff(50*time.Millisecond),
		urlscan.WithTimeout(10*time.Second),
	)).NoError(t)

	// Cancel after a short delay.
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	_, err := ts.Run(ctx, "urlscan_scan", map[string]any{"url": "https://example.com"})
	gt.Error(t, err)
}

func TestPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gt.String(t, r.Header.Get("API-Key")).Equal("ping-key")
		// Return 200 OK for the root.
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ts := gt.R1(urlscan.New(
		urlscan.WithAPIKey("ping-key"),
		urlscan.WithBaseURL(srv.URL),
		urlscan.WithHTTPClient(srv.Client()),
	)).NoError(t)

	gt.NoError(t, ts.Ping(context.Background()))
}

func TestPingServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ts := gt.R1(urlscan.New(
		urlscan.WithAPIKey("key"),
		urlscan.WithBaseURL(srv.URL),
		urlscan.WithHTTPClient(srv.Client()),
	)).NoError(t)

	err := ts.Ping(context.Background())
	gt.Error(t, err).Contains("server error")
}

// TestLive hits the real urlscan.io API. It runs only when TEST_URLSCAN_API_KEY
// is set.
func TestLive(t *testing.T) {
	apiKey, ok := os.LookupEnv("TEST_URLSCAN_API_KEY")
	if !ok {
		t.Skip("TEST_URLSCAN_API_KEY is not set")
	}

	ts := gt.R1(urlscan.New(urlscan.WithAPIKey(apiKey))).NoError(t)

	gt.NoError(t, ts.Ping(context.Background())).Required()

	// EICAR's anti-malware test file: the industry-standard, safe-to-fetch
	// "malicious" artifact. It is recognizable to any security tooling yet
	// harmless, and urlscan.io scans it without the "Scan prevented" block that
	// high-profile brand domains (e.g. google.com) trigger.
	result := gt.R1(ts.Run(context.Background(), "urlscan_scan", map[string]any{
		"url": "https://secure.eicar.org/eicar.com.txt",
	})).NoError(t)

	gt.Map(t, result).HasKey("page")
	_ = gt.R1(json.Marshal(result)).NoError(t)
}
