package shodan_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gollem-dev/tools/shodan"
	"github.com/m-mizutani/gt"
)

func TestNewRequiresAPIKey(t *testing.T) {
	_, err := shodan.New("")
	gt.Error(t, err).Contains("API key")
}

func TestSpecs(t *testing.T) {
	ts := gt.R1(shodan.New("dummy")).NoError(t)

	specs := gt.R1(ts.Specs(context.Background())).NoError(t)
	gt.Array(t, specs).Length(3)

	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
	}
	gt.Array(t, names).
		Has("shodan_host").
		Has("shodan_domain").
		Has("shodan_search")

	// shodan_search has both query and limit parameters.
	for _, s := range specs {
		if s.Name == "shodan_search" {
			gt.Map(t, s.Parameters).HasKey("query")
			gt.Map(t, s.Parameters).HasKey("limit")
		}
	}
}

func TestRunHost(t *testing.T) {
	var gotPath, gotKey string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.URL.Query().Get("key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ip":"8.8.8.8","ports":[53]}`))
	}))
	defer srv.Close()

	ts := gt.R1(shodan.New(
		"test-key",
		shodan.WithBaseURL(srv.URL),
		shodan.WithHTTPClient(srv.Client()),
	)).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "shodan_host", map[string]any{
		"target": "8.8.8.8",
	})).NoError(t)

	gt.String(t, gotPath).Equal("/shodan/host/8.8.8.8")
	gt.String(t, gotKey).Equal("test-key")
	gt.Map(t, result).HasKeyValue("ip", "8.8.8.8")
}

func TestRunDomain(t *testing.T) {
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domain":"example.com","subdomains":[]}`))
	}))
	defer srv.Close()

	ts := gt.R1(shodan.New(
		"key",
		shodan.WithBaseURL(srv.URL),
		shodan.WithHTTPClient(srv.Client()),
	)).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "shodan_domain", map[string]any{
		"target": "example.com",
	})).NoError(t)

	gt.String(t, gotPath).Equal("/dns/domain/example.com")
	gt.Map(t, result).HasKeyValue("domain", "example.com")
}

func TestRunSearch(t *testing.T) {
	var gotPath, gotQuery, gotLimit string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("query")
		gotLimit = r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"matches":[],"total":0}`))
	}))
	defer srv.Close()

	ts := gt.R1(shodan.New(
		"key",
		shodan.WithBaseURL(srv.URL),
		shodan.WithHTTPClient(srv.Client()),
	)).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "shodan_search", map[string]any{
		"query": "apache",
		"limit": float64(10),
	})).NoError(t)

	gt.String(t, gotPath).Equal("/shodan/host/search")
	gt.String(t, gotQuery).Equal("apache")
	gt.String(t, gotLimit).Equal("10")
	gt.Map(t, result).HasKey("matches")
}

func TestRunInvalidName(t *testing.T) {
	ts := gt.R1(shodan.New("dummy")).NoError(t)

	_, err := ts.Run(context.Background(), "shodan_unknown", map[string]any{"target": "x"})
	gt.Error(t, err).Contains("invalid function name")
}

func TestRunMissingTarget(t *testing.T) {
	ts := gt.R1(shodan.New("dummy")).NoError(t)

	_, err := ts.Run(context.Background(), "shodan_host", map[string]any{})
	gt.Error(t, err).Contains("target is required")
}

func TestRunHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"Invalid API key"}`))
	}))
	defer srv.Close()

	ts := gt.R1(shodan.New(
		"bad",
		shodan.WithBaseURL(srv.URL),
		shodan.WithHTTPClient(srv.Client()),
	)).NoError(t)

	_, err := ts.Run(context.Background(), "shodan_host", map[string]any{"target": "1.2.3.4"})
	gt.Error(t, err).Contains("failed to query Shodan")
}

func TestRunAPIErrorInBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":"No information available for that IP."}`))
	}))
	defer srv.Close()

	ts := gt.R1(shodan.New(
		"key",
		shodan.WithBaseURL(srv.URL),
		shodan.WithHTTPClient(srv.Client()),
	)).NoError(t)

	_, err := ts.Run(context.Background(), "shodan_host", map[string]any{"target": "192.0.2.1"})
	gt.Error(t, err).Contains("Shodan API returned error")
}

func TestPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gt.Bool(t, strings.Contains(r.URL.Path, "8.8.8.8")).True()
		gt.String(t, r.URL.Query().Get("key")).Equal("k")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ip":"8.8.8.8"}`))
	}))
	defer srv.Close()

	ts := gt.R1(shodan.New(
		"k",
		shodan.WithBaseURL(srv.URL),
		shodan.WithHTTPClient(srv.Client()),
	)).NoError(t)

	gt.NoError(t, ts.Ping(context.Background()))
}

// TestLive hits the real Shodan API. It runs only when TEST_SHODAN_API_KEY is
// set.
func TestLive(t *testing.T) {
	apiKey, ok := os.LookupEnv("TEST_SHODAN_API_KEY")
	if !ok {
		t.Skip("TEST_SHODAN_API_KEY is not set")
	}

	ts := gt.R1(shodan.New(apiKey)).NoError(t)

	gt.NoError(t, ts.Ping(context.Background())).Required()

	t.Run("shodan_host", func(t *testing.T) {
		result := gt.R1(ts.Run(context.Background(), "shodan_host", map[string]any{
			"target": "8.8.8.8",
		})).NoError(t)
		gt.Map(t, result).HasKey("ip")
	})

	t.Run("shodan_domain", func(t *testing.T) {
		result := gt.R1(ts.Run(context.Background(), "shodan_domain", map[string]any{
			"target": "google.com",
		})).NoError(t)
		gt.Map(t, result).HasKey("domain")
	})

	// shodan_search consumes query credits and may require a paid Shodan plan.
	t.Run("shodan_search", func(t *testing.T) {
		result := gt.R1(ts.Run(context.Background(), "shodan_search", map[string]any{
			"query": "hostname:google.com",
		})).NoError(t)
		gt.Map(t, result).HasKey("matches")
	})
}
