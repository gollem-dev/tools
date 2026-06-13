package intune_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/gollem-dev/tools/intune"
	"github.com/m-mizutani/gt"
)

// tokenResponse is a helper to build a synthetic OAuth token response body.
func tokenResponse(token string) []byte {
	b, _ := json.Marshal(map[string]any{
		"access_token": token,
		"expires_in":   3600,
		"token_type":   "Bearer",
	})
	return b
}

// devicesResponse is a helper to build a synthetic Graph managed-devices response.
func devicesResponse(devices ...map[string]any) []byte {
	values := make([]any, len(devices))
	for i, d := range devices {
		values[i] = d
	}
	b, _ := json.Marshal(map[string]any{"value": values})
	return b
}

// TestNewMissingTenantID verifies that New errors when tenant ID is absent.
func TestNewMissingTenantID(t *testing.T) {
	_, err := intune.New(
		intune.WithClientID("cid"),
		intune.WithClientSecret("csecret"),
	)
	gt.Error(t, err).Contains("tenant ID is required")
}

// TestNewMissingClientID verifies that New errors when client ID is absent.
func TestNewMissingClientID(t *testing.T) {
	_, err := intune.New(
		intune.WithTenantID("tid"),
		intune.WithClientSecret("csecret"),
	)
	gt.Error(t, err).Contains("client ID is required")
}

// TestNewMissingClientSecret verifies that New errors when client secret is absent.
func TestNewMissingClientSecret(t *testing.T) {
	_, err := intune.New(
		intune.WithTenantID("tid"),
		intune.WithClientID("cid"),
	)
	gt.Error(t, err).Contains("client secret is required")
}

// TestSpecs verifies the tool specifications returned by Specs.
func TestSpecs(t *testing.T) {
	ts := gt.R1(intune.New(
		intune.WithTenantID("tid"),
		intune.WithClientID("cid"),
		intune.WithClientSecret("csecret"),
	)).NoError(t)

	specs := gt.R1(ts.Specs(context.Background())).NoError(t)
	gt.Array(t, specs).Length(2)

	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
	}
	gt.Array(t, names).Has("intune_devices_by_user").Has("intune_devices_by_hostname")
}

// TestRunInvalidName verifies that Run returns an error for unknown tool names.
func TestRunInvalidName(t *testing.T) {
	ts := gt.R1(intune.New(
		intune.WithTenantID("tid"),
		intune.WithClientID("cid"),
		intune.WithClientSecret("csecret"),
	)).NoError(t)
	_, err := ts.Run(context.Background(), "intune_unknown", map[string]any{})
	gt.Error(t, err).Contains("invalid function name")
}

// newMockServer creates an httptest server that:
//   - POST /token → returns a synthetic Bearer token.
//   - GET /deviceManagement/managedDevices → asserts the $filter param and
//     returns the provided device list.
//
// The returned bearer string and query string are captured for assertions.
func newMockServer(t *testing.T, respDevices []map[string]any) (srv *httptest.Server, gotFilter *string, gotBearer *string) {
	t.Helper()
	filter := ""
	bearer := ""
	gotFilter = &filter
	gotBearer = &bearer

	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/token"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(tokenResponse("test-access-token"))

		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "managedDevices"):
			*gotBearer = r.Header.Get("Authorization")
			q, _ := url.QueryUnescape(r.URL.Query().Get("$filter"))
			*gotFilter = q
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(devicesResponse(respDevices...))

		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "auditLogs/signIns"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"value":[{"ipAddress":"203.0.113.5","createdDateTime":"2024-01-01T00:00:00Z","clientAppUsed":"Browser"}]}`))

		default:
			http.NotFound(w, r)
		}
	}))
	return srv, gotFilter, gotBearer
}

// TestRunDevicesByUser verifies the $filter and Bearer header for the by-user tool.
func TestRunDevicesByUser(t *testing.T) {
	device := map[string]any{
		"id":                "dev-1",
		"deviceName":        "LAPTOP-001",
		"userPrincipalName": "alice@example.com",
		"complianceState":   "compliant",
		"operatingSystem":   "macOS",
	}
	srv, gotFilter, gotBearer := newMockServer(t, []map[string]any{device})
	defer srv.Close()

	ts := gt.R1(intune.New(
		intune.WithTenantID("mytenant"),
		intune.WithClientID("myclient"),
		intune.WithClientSecret("mysecret"),
		intune.WithTokenEndpoint(srv.URL+"/mytenant/oauth2/v2.0/token"),
		intune.WithBaseURL(srv.URL),
		intune.WithHTTPClient(srv.Client()),
	)).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "intune_devices_by_user", map[string]any{
		"user_principal_name": "alice@example.com",
	})).NoError(t)

	gt.String(t, *gotBearer).Equal("Bearer test-access-token")
	gt.String(t, *gotFilter).Contains("userPrincipalName eq 'alice@example.com'")

	gt.Map(t, result).HasKey("devices")
	gt.Map(t, result).HasKey("totalDevices")

	devices, ok := result["devices"].([]any)
	gt.Bool(t, ok).True()
	gt.Array(t, devices).Length(1)

	// Recent sign-in IP history must be included (regression guard).
	gt.Map(t, result).HasKey("signInHistory")
	signIns, ok := result["signInHistory"].([]any)
	gt.Bool(t, ok).True()
	gt.Array(t, signIns).Length(1)
}

// TestRunDevicesByHostname verifies the $filter and Bearer header for the by-hostname tool.
func TestRunDevicesByHostname(t *testing.T) {
	device := map[string]any{
		"id":                "dev-2",
		"deviceName":        "SERVER-001",
		"userPrincipalName": "bob@example.com",
		"complianceState":   "nonCompliant",
		"operatingSystem":   "Windows",
	}
	srv, gotFilter, gotBearer := newMockServer(t, []map[string]any{device})
	defer srv.Close()

	ts := gt.R1(intune.New(
		intune.WithTenantID("mytenant"),
		intune.WithClientID("myclient"),
		intune.WithClientSecret("mysecret"),
		intune.WithTokenEndpoint(srv.URL+"/mytenant/oauth2/v2.0/token"),
		intune.WithBaseURL(srv.URL),
		intune.WithHTTPClient(srv.Client()),
	)).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "intune_devices_by_hostname", map[string]any{
		"device_name": "SERVER-001",
	})).NoError(t)

	gt.String(t, *gotBearer).Equal("Bearer test-access-token")
	gt.String(t, *gotFilter).Contains("deviceName eq 'SERVER-001'")

	gt.Map(t, result).HasKey("devices")
	gt.Map(t, result).HasKey("totalDevices")

	// Sign-in history is derived from the first matched device's UPN.
	gt.Map(t, result).HasKey("signInHistory")
	signIns, ok := result["signInHistory"].([]any)
	gt.Bool(t, ok).True()
	gt.Array(t, signIns).Length(1)
}

// TestRunDevicesByUserMissingParam verifies that Run errors when UPN is absent.
func TestRunDevicesByUserMissingParam(t *testing.T) {
	ts := gt.R1(intune.New(
		intune.WithTenantID("tid"),
		intune.WithClientID("cid"),
		intune.WithClientSecret("csecret"),
	)).NoError(t)
	_, err := ts.Run(context.Background(), "intune_devices_by_user", map[string]any{})
	gt.Error(t, err).Contains("user_principal_name is required")
}

// TestRunDevicesByHostnameMissingParam verifies that Run errors when device name is absent.
func TestRunDevicesByHostnameMissingParam(t *testing.T) {
	ts := gt.R1(intune.New(
		intune.WithTenantID("tid"),
		intune.WithClientID("cid"),
		intune.WithClientSecret("csecret"),
	)).NoError(t)
	_, err := ts.Run(context.Background(), "intune_devices_by_hostname", map[string]any{})
	gt.Error(t, err).Contains("device_name is required")
}

// TestPing verifies that Ping acquires a token and returns nil on success.
func TestPing(t *testing.T) {
	var tokenRequested bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/token") {
			tokenRequested = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(tokenResponse("ping-token"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	ts := gt.R1(intune.New(
		intune.WithTenantID("tid"),
		intune.WithClientID("cid"),
		intune.WithClientSecret("csecret"),
		intune.WithTokenEndpoint(srv.URL+"/tid/oauth2/v2.0/token"),
		intune.WithHTTPClient(srv.Client()),
	)).NoError(t)

	gt.NoError(t, ts.Ping(context.Background()))
	gt.Bool(t, tokenRequested).True()
}

// TestPingTokenFailure verifies that Ping propagates token acquisition errors.
func TestPingTokenFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer srv.Close()

	ts := gt.R1(intune.New(
		intune.WithTenantID("tid"),
		intune.WithClientID("bad-client"),
		intune.WithClientSecret("bad-secret"),
		intune.WithTokenEndpoint(srv.URL+"/tid/oauth2/v2.0/token"),
		intune.WithHTTPClient(srv.Client()),
	)).NoError(t)

	err := ts.Ping(context.Background())
	gt.Error(t, err).Contains("ping failed")
}

// TestTokenAcquiredOnce verifies that a valid cached token is reused without a second
// network round-trip. The mock server counts token requests; exactly one is expected.
func TestTokenAcquiredOnce(t *testing.T) {
	tokenRequests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/token"):
			tokenRequests++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(tokenResponse("cached-token"))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "managedDevices"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(devicesResponse())
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ts := gt.R1(intune.New(
		intune.WithTenantID("tid"),
		intune.WithClientID("cid"),
		intune.WithClientSecret("csecret"),
		intune.WithTokenEndpoint(srv.URL+"/tid/oauth2/v2.0/token"),
		intune.WithBaseURL(srv.URL),
		intune.WithHTTPClient(srv.Client()),
	)).NoError(t)

	// Two back-to-back calls must share the same token.
	gt.R1(ts.Run(context.Background(), "intune_devices_by_user", map[string]any{
		"user_principal_name": "x@example.com",
	})).NoError(t)
	gt.R1(ts.Run(context.Background(), "intune_devices_by_user", map[string]any{
		"user_principal_name": "y@example.com",
	})).NoError(t)

	gt.Number(t, tokenRequests).Equal(1)
}

// TestLive hits the real Microsoft Graph API. It runs only when all required
// TEST_INTUNE_* environment variables are set:
//
//   - TEST_INTUNE_TENANT_ID     – Azure AD tenant ID (required)
//   - TEST_INTUNE_CLIENT_ID     – Azure AD application (client) ID (required)
//   - TEST_INTUNE_CLIENT_SECRET – Azure AD client secret (required)
//   - TEST_INTUNE_USER          – UPN / email used for intune_devices_by_user (required)
//   - TEST_INTUNE_HOSTNAME      – device hostname used for intune_devices_by_hostname (optional;
//     when absent the by-hostname sub-case is skipped but the by-user sub-case still runs)
func TestLive(t *testing.T) {
	tenantID, ok := os.LookupEnv("TEST_INTUNE_TENANT_ID")
	if !ok {
		t.Skip("TEST_INTUNE_TENANT_ID is not set")
	}
	clientID, ok := os.LookupEnv("TEST_INTUNE_CLIENT_ID")
	if !ok {
		t.Skip("TEST_INTUNE_CLIENT_ID is not set")
	}
	clientSecret, ok := os.LookupEnv("TEST_INTUNE_CLIENT_SECRET")
	if !ok {
		t.Skip("TEST_INTUNE_CLIENT_SECRET is not set")
	}
	testUser, ok := os.LookupEnv("TEST_INTUNE_USER")
	if !ok {
		t.Skip("TEST_INTUNE_USER is not set")
	}

	ts := gt.R1(intune.New(
		intune.WithTenantID(tenantID),
		intune.WithClientID(clientID),
		intune.WithClientSecret(clientSecret),
	)).NoError(t)

	gt.NoError(t, ts.Ping(context.Background())).Required()

	t.Run("intune_devices_by_user", func(t *testing.T) {
		result := gt.R1(ts.Run(context.Background(), "intune_devices_by_user", map[string]any{
			"user_principal_name": testUser,
		})).NoError(t)

		gt.Map(t, result).HasKey("devices")
		gt.Map(t, result).HasKey("totalDevices")

		// Verify the payload round-trips to JSON without error.
		_ = gt.R1(json.Marshal(result)).NoError(t)
	})

	t.Run("intune_devices_by_hostname", func(t *testing.T) {
		hostname, ok := os.LookupEnv("TEST_INTUNE_HOSTNAME")
		if !ok {
			t.Log("TEST_INTUNE_HOSTNAME is not set; skipping intune_devices_by_hostname sub-case")
			t.Skip()
		}

		result := gt.R1(ts.Run(context.Background(), "intune_devices_by_hostname", map[string]any{
			"device_name": hostname,
		})).NoError(t)

		gt.Map(t, result).HasKey("devices")
		gt.Map(t, result).HasKey("totalDevices")

		// Verify the payload round-trips to JSON without error.
		_ = gt.R1(json.Marshal(result)).NoError(t)
	})
}
