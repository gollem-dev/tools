package ipdb_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/gollem-dev/tools/ipdb"
	"github.com/m-mizutani/gt"
)

func TestNewRequiresAPIKey(t *testing.T) {
	_, err := ipdb.New("")
	gt.Error(t, err).Contains("API key")
}

func TestSpecs(t *testing.T) {
	ts := gt.R1(ipdb.New("dummy")).NoError(t)

	specs := gt.R1(ts.Specs(context.Background())).NoError(t)
	gt.Array(t, specs).Length(1)

	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
		gt.Map(t, s.Parameters).HasKey("target")
	}
	gt.Array(t, names).Has("ipdb_check")
}

func TestRun(t *testing.T) {
	var gotQuery url.Values
	var gotKey, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		gotKey = r.Header.Get("Key")
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ipAddress":"1.2.3.4","abuseConfidenceScore":0}}`))
	}))
	defer srv.Close()

	ts := gt.R1(ipdb.New(
		"test-key",
		ipdb.WithBaseURL(srv.URL),
		ipdb.WithHTTPClient(srv.Client()),
	)).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "ipdb_check", map[string]any{"target": "1.2.3.4"})).NoError(t)

	gt.Map(t, result).HasKey("data")
	gt.String(t, gotKey).Equal("test-key")
	gt.String(t, gotAccept).Equal("application/json")
	gt.String(t, gotQuery.Get("ipAddress")).Equal("1.2.3.4")
}

func TestRunWithMaxAge(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ipAddress":"1.2.3.4","abuseConfidenceScore":0}}`))
	}))
	defer srv.Close()

	ts := gt.R1(ipdb.New(
		"test-key",
		ipdb.WithBaseURL(srv.URL),
		ipdb.WithHTTPClient(srv.Client()),
	)).NoError(t)

	// LLM agents pass JSON numbers as float64.
	_ = gt.R1(ts.Run(context.Background(), "ipdb_check", map[string]any{
		"target":       "1.2.3.4",
		"maxAgeInDays": float64(30),
	})).NoError(t)

	gt.String(t, gotQuery.Get("maxAgeInDays")).Equal("30")
}

func TestRunInvalidName(t *testing.T) {
	ts := gt.R1(ipdb.New("dummy")).NoError(t)
	_, err := ts.Run(context.Background(), "ipdb_unknown", map[string]any{"target": "1.2.3.4"})
	gt.Error(t, err).Contains("invalid function name")
}

func TestRunMissingTarget(t *testing.T) {
	ts := gt.R1(ipdb.New("dummy")).NoError(t)
	_, err := ts.Run(context.Background(), "ipdb_check", map[string]any{})
	gt.Error(t, err).Contains("target is required")
}

func TestRunHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"errors":[{"detail":"Authentication failed","status":"401"}]}`))
	}))
	defer srv.Close()

	ts := gt.R1(ipdb.New(
		"bad-key",
		ipdb.WithBaseURL(srv.URL),
		ipdb.WithHTTPClient(srv.Client()),
	)).NoError(t)

	_, err := ts.Run(context.Background(), "ipdb_check", map[string]any{"target": "1.2.3.4"})
	gt.Error(t, err).Contains("failed to query AbuseIPDB")
}

func TestPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gt.Bool(t, strings.Contains(r.URL.RawQuery, "8.8.8.8")).True()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	ts := gt.R1(ipdb.New(
		"k",
		ipdb.WithBaseURL(srv.URL),
		ipdb.WithHTTPClient(srv.Client()),
	)).NoError(t)

	gt.NoError(t, ts.Ping(context.Background()))
}

// TestLive hits the real AbuseIPDB API. It runs only when TEST_IPDB_API_KEY is set.
func TestLive(t *testing.T) {
	apiKey, ok := os.LookupEnv("TEST_IPDB_API_KEY")
	if !ok {
		t.Skip("TEST_IPDB_API_KEY is not set")
	}

	ts := gt.R1(ipdb.New(apiKey)).NoError(t)

	gt.NoError(t, ts.Ping(context.Background())).Required()

	result := gt.R1(ts.Run(context.Background(), "ipdb_check", map[string]any{"target": "8.8.8.8"})).NoError(t)
	gt.Map(t, result).HasKey("data")

	// Sanity-check the payload is decodable JSON with the expected shape.
	_ = gt.R1(json.Marshal(result)).NoError(t)
}
