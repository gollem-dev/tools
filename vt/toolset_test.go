package vt_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gollem-dev/tools/vt"
	"github.com/m-mizutani/gt"
)

func TestNewRequiresAPIKey(t *testing.T) {
	_, err := vt.New()
	gt.Error(t, err).Contains("API key")
}

func TestSpecs(t *testing.T) {
	ts := gt.R1(vt.New(vt.WithAPIKey("dummy"))).NoError(t)

	specs := gt.R1(ts.Specs(context.Background())).NoError(t)
	gt.Array(t, specs).Length(4)

	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
		gt.Map(t, s.Parameters).HasKey("target")
	}
	gt.Array(t, names).
		Has("vt_ip").Has("vt_domain").Has("vt_file_hash").Has("vt_url")
}

func TestRun(t *testing.T) {
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-apikey")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"1.2.3.4","type":"ip_address"}}`))
	}))
	defer srv.Close()

	ts := gt.R1(vt.New(
		vt.WithAPIKey("test-key"),
		vt.WithBaseURL(srv.URL),
		vt.WithHTTPClient(srv.Client()),
	)).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "vt_ip", map[string]any{"target": "1.2.3.4"})).NoError(t)

	gt.Map(t, result).HasKey("data")
	gt.String(t, gotKey).Equal("test-key")
	gt.String(t, gotPath).Equal("/ip_addresses/1.2.3.4")
}

func TestRunDomain(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"example.com","type":"domain"}}`))
	}))
	defer srv.Close()

	ts := gt.R1(vt.New(
		vt.WithAPIKey("test-key"),
		vt.WithBaseURL(srv.URL),
		vt.WithHTTPClient(srv.Client()),
	)).NoError(t)

	_ = gt.R1(ts.Run(context.Background(), "vt_domain", map[string]any{"target": "example.com"})).NoError(t)
	gt.String(t, gotPath).Equal("/domains/example.com")
}

func TestRunFileHash(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"abc123","type":"file"}}`))
	}))
	defer srv.Close()

	ts := gt.R1(vt.New(
		vt.WithAPIKey("test-key"),
		vt.WithBaseURL(srv.URL),
		vt.WithHTTPClient(srv.Client()),
	)).NoError(t)

	_ = gt.R1(ts.Run(context.Background(), "vt_file_hash", map[string]any{"target": "abc123"})).NoError(t)
	gt.String(t, gotPath).Equal("/files/abc123")
}

func TestRunURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"http://example.com","type":"url"}}`))
	}))
	defer srv.Close()

	ts := gt.R1(vt.New(
		vt.WithAPIKey("test-key"),
		vt.WithBaseURL(srv.URL),
		vt.WithHTTPClient(srv.Client()),
	)).NoError(t)

	_ = gt.R1(ts.Run(context.Background(), "vt_url", map[string]any{"target": "http://example.com"})).NoError(t)
	// VirusTotal requires vt_url identifiers to be base64url (RawURLEncoding, no
	// padding) encoded. Base64url characters are all path-safe so no
	// percent-escaping is needed.
	wantID := base64.RawURLEncoding.EncodeToString([]byte("http://example.com"))
	gt.String(t, gotPath).Equal("/urls/" + wantID)
}

func TestRunInvalidName(t *testing.T) {
	ts := gt.R1(vt.New(vt.WithAPIKey("dummy"))).NoError(t)
	_, err := ts.Run(context.Background(), "vt_unknown", map[string]any{"target": "x"})
	gt.Error(t, err).Contains("invalid function name")
}

func TestRunMissingTarget(t *testing.T) {
	ts := gt.R1(vt.New(vt.WithAPIKey("dummy"))).NoError(t)
	_, err := ts.Run(context.Background(), "vt_ip", map[string]any{})
	gt.Error(t, err).Contains("target is required")
}

func TestRunHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"WrongCredentialsError"}}`))
	}))
	defer srv.Close()

	ts := gt.R1(vt.New(
		vt.WithAPIKey("bad-key"),
		vt.WithBaseURL(srv.URL),
		vt.WithHTTPClient(srv.Client()),
	)).NoError(t)

	_, err := ts.Run(context.Background(), "vt_ip", map[string]any{"target": "1.2.3.4"})
	gt.Error(t, err).Contains("failed to query VirusTotal")
}

func TestPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gt.Bool(t, strings.Contains(r.URL.Path, "8.8.8.8")).True()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	ts := gt.R1(vt.New(
		vt.WithAPIKey("k"),
		vt.WithBaseURL(srv.URL),
		vt.WithHTTPClient(srv.Client()),
	)).NoError(t)

	gt.NoError(t, ts.Ping(context.Background()))
}

// TestLive hits the real VirusTotal API. It runs only when TEST_VT_API_KEY is set.
func TestLive(t *testing.T) {
	apiKey, ok := os.LookupEnv("TEST_VT_API_KEY")
	if !ok {
		t.Skip("TEST_VT_API_KEY is not set")
	}

	ts := gt.R1(vt.New(vt.WithAPIKey(apiKey))).NoError(t)

	gt.NoError(t, ts.Ping(context.Background())).Required()

	t.Run("vt_ip", func(t *testing.T) {
		result := gt.R1(ts.Run(context.Background(), "vt_ip", map[string]any{"target": "8.8.8.8"})).NoError(t)
		gt.Map(t, result).HasKey("data")
		_ = gt.R1(json.Marshal(result)).NoError(t)
	})

	t.Run("vt_domain", func(t *testing.T) {
		result := gt.R1(ts.Run(context.Background(), "vt_domain", map[string]any{"target": "google.com"})).NoError(t)
		gt.Map(t, result).HasKey("data")
		_ = gt.R1(json.Marshal(result)).NoError(t)
	})

	t.Run("vt_file_hash", func(t *testing.T) {
		// EICAR test-file SHA256 — universally known, safe to query.
		result := gt.R1(ts.Run(context.Background(), "vt_file_hash", map[string]any{
			"target": "275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f",
		})).NoError(t)
		gt.Map(t, result).HasKey("data")
		_ = gt.R1(json.Marshal(result)).NoError(t)
	})

	t.Run("vt_url", func(t *testing.T) {
		// The indicator is base64url-encoded by Run before hitting the API.
		result := gt.R1(ts.Run(context.Background(), "vt_url", map[string]any{"target": "https://www.google.com/"})).NoError(t)
		gt.Map(t, result).HasKey("data")
		_ = gt.R1(json.Marshal(result)).NoError(t)
	})
}
