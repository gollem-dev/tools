package abusech_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/gollem-dev/tools/abusech"
	"github.com/m-mizutani/gt"
)

// validResponse is a minimal MalwareBazaar get_info success payload.
const validResponse = `{"query_status":"ok","data":[{"sha256_hash":"b3d9ea5fb70f1b6ecf74f36e3a88da0ac44dd8afb4a4d70e8f15fa10f6fa3f7b","file_name":"sample.exe"}]}`

func TestNewRequiresAPIKey(t *testing.T) {
	_, err := abusech.New()
	gt.Error(t, err).Contains("API key")
}

func TestSpecs(t *testing.T) {
	ts := gt.R1(abusech.New(abusech.WithAPIKey("dummy"))).NoError(t)

	specs := gt.R1(ts.Specs(context.Background())).NoError(t)
	gt.Array(t, specs).Length(1)

	gt.Value(t, specs[0].Name).Equal("abusech.bazaar.query")
	gt.Map(t, specs[0].Parameters).HasKey("hash")
}

func TestRun(t *testing.T) {
	var gotPath, gotAuthKey, gotContentType, gotForm string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthKey = r.Header.Get("Auth-Key")
		gotContentType = r.Header.Get("Content-Type")

		if err := r.ParseForm(); err == nil {
			gotForm = r.FormValue("query") + ":" + r.FormValue("hash")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validResponse))
	}))
	defer srv.Close()

	ts := gt.R1(abusech.New(
		abusech.WithAPIKey("test-key"),
		abusech.WithBaseURL(srv.URL),
		abusech.WithHTTPClient(srv.Client()),
	)).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "abusech.bazaar.query", map[string]any{
		"hash": "b3d9ea5fb70f1b6ecf74f36e3a88da0ac44dd8afb4a4d70e8f15fa10f6fa3f7b",
	})).NoError(t)

	// Verify request shape.
	gt.String(t, gotPath).Equal("/")
	gt.String(t, gotAuthKey).Equal("test-key")
	gt.String(t, gotContentType).Contains("application/x-www-form-urlencoded")
	gt.String(t, gotForm).Equal("get_info:b3d9ea5fb70f1b6ecf74f36e3a88da0ac44dd8afb4a4d70e8f15fa10f6fa3f7b")

	// Verify response is passed through.
	gt.Map(t, result).HasKeyValue("query_status", "ok")
}

func TestRunInvalidName(t *testing.T) {
	ts := gt.R1(abusech.New(abusech.WithAPIKey("dummy"))).NoError(t)

	_, err := ts.Run(context.Background(), "abusech.unknown", map[string]any{"hash": "abc"})
	gt.Error(t, err).Contains("invalid function name")
}

func TestRunMissingHash(t *testing.T) {
	ts := gt.R1(abusech.New(abusech.WithAPIKey("dummy"))).NoError(t)

	_, err := ts.Run(context.Background(), "abusech.bazaar.query", map[string]any{})
	gt.Error(t, err).Contains("hash is required")
}

func TestRunHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`forbidden`))
	}))
	defer srv.Close()

	ts := gt.R1(abusech.New(
		abusech.WithAPIKey("bad"),
		abusech.WithBaseURL(srv.URL),
		abusech.WithHTTPClient(srv.Client()),
	)).NoError(t)

	_, err := ts.Run(context.Background(), "abusech.bazaar.query", map[string]any{"hash": "abc123"})
	gt.Error(t, err).Contains("unexpected status from MalwareBazaar")
}

func TestRunAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"query_status":"error","error_message":"illegal_hash"}`))
	}))
	defer srv.Close()

	ts := gt.R1(abusech.New(
		abusech.WithAPIKey("key"),
		abusech.WithBaseURL(srv.URL),
		abusech.WithHTTPClient(srv.Client()),
	)).NoError(t)

	_, err := ts.Run(context.Background(), "abusech.bazaar.query", map[string]any{"hash": "notahash"})
	gt.Error(t, err).Contains("MalwareBazaar API returned error")
}

func TestPing(t *testing.T) {
	var gotFormQuery string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err == nil {
			gotFormQuery = r.FormValue("query")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validResponse))
	}))
	defer srv.Close()

	// Parse the server URL to validate it is used as baseURL.
	parsedURL, err := url.Parse(srv.URL)
	gt.NoError(t, err)
	gt.Value(t, parsedURL).NotNil()

	ts := gt.R1(abusech.New(
		abusech.WithAPIKey("k"),
		abusech.WithBaseURL(srv.URL),
		abusech.WithHTTPClient(srv.Client()),
	)).NoError(t)

	gt.NoError(t, ts.Ping(context.Background()))
	gt.String(t, gotFormQuery).Equal("get_info")
}

// TestLive hits the real MalwareBazaar API. It runs only when
// TEST_ABUSECH_API_KEY is set.
func TestLive(t *testing.T) {
	apiKey, ok := os.LookupEnv("TEST_ABUSECH_API_KEY")
	if !ok {
		t.Skip("TEST_ABUSECH_API_KEY is not set")
	}

	ts := gt.R1(abusech.New(abusech.WithAPIKey(apiKey))).NoError(t)

	gt.NoError(t, ts.Ping(context.Background())).Required()

	// Query the same well-known hash used for Ping.
	result := gt.R1(ts.Run(context.Background(), "abusech.bazaar.query", map[string]any{
		"hash": "b3d9ea5fb70f1b6ecf74f36e3a88da0ac44dd8afb4a4d70e8f15fa10f6fa3f7b",
	})).NoError(t)
	gt.Map(t, result).HasKey("query_status")
}
