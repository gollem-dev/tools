package otx_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gollem-dev/tools/otx"
	"github.com/m-mizutani/gt"
)

func TestNewRequiresAPIKey(t *testing.T) {
	_, err := otx.New()
	gt.Error(t, err).Contains("API key")
}

func TestSpecs(t *testing.T) {
	ts := gt.R1(otx.New(otx.WithAPIKey("dummy"))).NoError(t)

	specs := gt.R1(ts.Specs(context.Background())).NoError(t)
	gt.Array(t, specs).Length(5)

	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
		gt.Map(t, s.Parameters).HasKey("target")
	}
	gt.Array(t, names).
		Has("otx_ipv4").Has("otx_ipv6").Has("otx_domain").
		Has("otx_hostname").Has("otx_file_hash")
}

func TestRun(t *testing.T) {
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("X-OTX-API-KEY")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"indicator":"1.2.3.4","pulse_info":{"count":2}}`))
	}))
	defer srv.Close()

	ts := gt.R1(otx.New(
		otx.WithAPIKey("test-key"),
		otx.WithBaseURL(srv.URL),
		otx.WithHTTPClient(srv.Client()),
	)).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "otx_ipv4", map[string]any{"target": "1.2.3.4"})).NoError(t)

	gt.Map(t, result).HasKeyValue("indicator", "1.2.3.4")
	gt.String(t, gotKey).Equal("test-key")
	gt.String(t, gotPath).Equal("/indicators/IPv4/1.2.3.4/general")
}

func TestRunInvalidName(t *testing.T) {
	ts := gt.R1(otx.New(otx.WithAPIKey("dummy"))).NoError(t)
	_, err := ts.Run(context.Background(), "otx_unknown", map[string]any{"target": "x"})
	gt.Error(t, err).Contains("invalid function name")
}

func TestRunMissingTarget(t *testing.T) {
	ts := gt.R1(otx.New(otx.WithAPIKey("dummy"))).NoError(t)
	_, err := ts.Run(context.Background(), "otx_ipv4", map[string]any{})
	gt.Error(t, err).Contains("target is required")
}

func TestRunHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`forbidden`))
	}))
	defer srv.Close()

	ts := gt.R1(otx.New(
		otx.WithAPIKey("bad"),
		otx.WithBaseURL(srv.URL),
		otx.WithHTTPClient(srv.Client()),
	)).NoError(t)

	_, err := ts.Run(context.Background(), "otx_domain", map[string]any{"target": "example.com"})
	gt.Error(t, err).Contains("failed to query OTX")
}

func TestPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gt.Bool(t, strings.Contains(r.URL.Path, "8.8.8.8")).True()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	ts := gt.R1(otx.New(
		otx.WithAPIKey("k"),
		otx.WithBaseURL(srv.URL),
		otx.WithHTTPClient(srv.Client()),
	)).NoError(t)

	gt.NoError(t, ts.Ping(context.Background()))
}

// TestLive hits the real OTX API. It runs only when TEST_OTX_API_KEY is set.
func TestLive(t *testing.T) {
	apiKey, ok := os.LookupEnv("TEST_OTX_API_KEY")
	if !ok {
		t.Skip("TEST_OTX_API_KEY is not set")
	}

	ts := gt.R1(otx.New(otx.WithAPIKey(apiKey))).NoError(t)

	gt.NoError(t, ts.Ping(context.Background())).Required()

	t.Run("otx_ipv4", func(t *testing.T) {
		result := gt.R1(ts.Run(context.Background(), "otx_ipv4", map[string]any{"target": "8.8.8.8"})).NoError(t)
		gt.Map(t, result).HasKey("indicator")
		_ = gt.R1(json.Marshal(result)).NoError(t)
	})

	t.Run("otx_ipv6", func(t *testing.T) {
		result := gt.R1(ts.Run(context.Background(), "otx_ipv6", map[string]any{"target": "2001:4860:4860::8888"})).NoError(t)
		gt.Map(t, result).HasKey("indicator")
		_ = gt.R1(json.Marshal(result)).NoError(t)
	})

	t.Run("otx_domain", func(t *testing.T) {
		result := gt.R1(ts.Run(context.Background(), "otx_domain", map[string]any{"target": "google.com"})).NoError(t)
		gt.Map(t, result).HasKey("indicator")
		_ = gt.R1(json.Marshal(result)).NoError(t)
	})

	t.Run("otx_hostname", func(t *testing.T) {
		result := gt.R1(ts.Run(context.Background(), "otx_hostname", map[string]any{"target": "www.google.com"})).NoError(t)
		gt.Map(t, result).HasKey("indicator")
		_ = gt.R1(json.Marshal(result)).NoError(t)
	})

	t.Run("otx_file_hash", func(t *testing.T) {
		// EICAR test-file SHA256 — universally known, safe to query.
		result := gt.R1(ts.Run(context.Background(), "otx_file_hash", map[string]any{
			"target": "275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f",
		})).NoError(t)
		gt.Map(t, result).HasKey("indicator")
		_ = gt.R1(json.Marshal(result)).NoError(t)
	})
}
