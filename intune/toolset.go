// Package intune provides a gollem.ToolSet for querying Microsoft Intune managed
// device information via the Microsoft Graph API. Authentication uses the OAuth
// 2.0 Client Credentials Flow.
package intune

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gollem-dev/gollem"
	"github.com/m-mizutani/goerr/v2"
)

const (
	defaultGraphBaseURL     = "https://graph.microsoft.com/v1.0"
	defaultTokenEndpointFmt = "https://login.microsoftonline.com/%s/oauth2/v2.0/token"
	graphDefaultScope       = "https://graph.microsoft.com/.default"
	// tokenExpiryBuffer is subtracted from the reported expiry so tokens are
	// refreshed slightly before they actually expire.
	tokenExpiryBuffer = 5 * time.Minute
)

// ToolSet implements gollem.ToolSet for Microsoft Intune / Graph API lookups.
// Fields are unexported; configure via Option.
type ToolSet struct {
	tenantID     string
	clientID     string
	clientSecret string
	baseURL      string
	// tokenEndpoint is the OAuth2 token URL. Derived from tenantID in New; can
	// be overridden via WithTokenEndpoint for testing.
	tokenEndpoint string
	client        *http.Client
	logger        *slog.Logger
	tools         []gollem.Tool
	toolByName    map[string]gollem.Tool

	// Token cache — guarded by mu.
	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time
}

// devicesByUserInput holds the typed arguments for the intune_devices_by_user tool.
type devicesByUserInput struct {
	UserPrincipalName string `json:"user_principal_name" description:"User's email address or UPN" required:"true"`
}

// devicesByHostnameInput holds the typed arguments for the intune_devices_by_hostname tool.
type devicesByHostnameInput struct {
	DeviceName string `json:"device_name" description:"Device hostname to search" required:"true"`
}

// Startup assertions: a malformed input/output type (a broken struct tag, a
// non-object kind) is a programming error that should surface at init rather
// than on the first LLM call. See gollem docs "Validating Tool Types".
var (
	_ = gollem.MustToolSchema[devicesByUserInput, map[string]any]()
	_ = gollem.MustToolSchema[devicesByHostnameInput, map[string]any]()
)

var _ gollem.ToolSet = (*ToolSet)(nil)

// Option configures a ToolSet.
type Option func(*ToolSet)

// WithBaseURL overrides the Microsoft Graph API base URL
// (default: https://graph.microsoft.com/v1.0).
func WithBaseURL(baseURL string) Option {
	return func(t *ToolSet) {
		if baseURL != "" {
			t.baseURL = baseURL
		}
	}
}

// WithTokenEndpoint overrides the OAuth2 token endpoint. The default is derived
// from the tenant ID. This is intended for unit tests that point at httptest servers.
func WithTokenEndpoint(endpoint string) Option {
	return func(t *ToolSet) {
		if endpoint != "" {
			t.tokenEndpoint = endpoint
		}
	}
}

// WithHTTPClient overrides the HTTP client used for all requests.
func WithHTTPClient(client *http.Client) Option {
	return func(t *ToolSet) {
		if client != nil {
			t.client = client
		}
	}
}

// WithLogger sets the logger. A nil logger keeps the default (slog.Default()).
func WithLogger(logger *slog.Logger) Option {
	return func(t *ToolSet) {
		if logger != nil {
			t.logger = logger
		}
	}
}

// New constructs the ToolSet with the required Azure AD credentials.
// tenantID, clientID, and clientSecret must all be non-empty.
// It only validates static configuration; use Ping to verify connectivity and credentials.
func New(tenantID string, clientID string, clientSecret string, opts ...Option) (*ToolSet, error) {
	if tenantID == "" {
		return nil, goerr.New("Intune tenant ID is required")
	}
	if clientID == "" {
		return nil, goerr.New("Intune client ID is required")
	}
	if clientSecret == "" {
		return nil, goerr.New("Intune client secret is required")
	}

	t := &ToolSet{
		tenantID:     tenantID,
		clientID:     clientID,
		clientSecret: clientSecret,
		baseURL:      defaultGraphBaseURL,
		client:       http.DefaultClient,
		logger:       slog.Default(),
	}
	for _, opt := range opts {
		opt(t)
	}

	// Derive the default token endpoint from tenantID (pure string build, no network).
	if t.tokenEndpoint == "" {
		t.tokenEndpoint = fmt.Sprintf(defaultTokenEndpointFmt, t.tenantID)
	}

	t.tools = t.buildTools()
	t.toolByName = indexTools(t.tools)

	return t, nil
}

// indexTools builds a name->tool lookup so Run dispatches in O(1) instead of
// scanning (and re-deriving Spec()) on every call. The map is built once at
// construction and never mutated, so it is safe for concurrent Run calls.
func indexTools(tools []gollem.Tool) map[string]gollem.Tool {
	byName := make(map[string]gollem.Tool, len(tools))
	for _, tool := range tools {
		byName[tool.Spec().Name] = tool
	}
	return byName
}

// buildTools constructs the typed Intune tools. Each tool has a distinct input
// struct so schema and Run decode share a single source of truth.
// MustNewTool is used because the In/Out types are static: a build failure is a
// programming error (already guarded by the package-level MustToolSchema), not a
// runtime condition New should report.
func (t *ToolSet) buildTools() []gollem.Tool {
	toolByUser := gollem.MustNewTool(
		"intune_devices_by_user",
		"Search Intune managed devices by user email address or UPN (User Principal Name). Returns device details including compliance state, OS, encryption, and recent sign-in IP history.",
		func(ctx context.Context, in devicesByUserInput) (map[string]any, error) {
			if in.UserPrincipalName == "" {
				return nil, goerr.New("user_principal_name is required", goerr.V("args", in))
			}
			return t.searchDevicesByUser(ctx, in.UserPrincipalName)
		},
	)

	toolByHostname := gollem.MustNewTool(
		"intune_devices_by_hostname",
		"Search Intune managed device by device hostname. Returns device details including compliance state, OS, encryption, and owner information.",
		func(ctx context.Context, in devicesByHostnameInput) (map[string]any, error) {
			if in.DeviceName == "" {
				return nil, goerr.New("device_name is required", goerr.V("args", in))
			}
			return t.searchDevicesByHostname(ctx, in.DeviceName)
		},
	)

	return []gollem.Tool{toolByUser, toolByHostname}
}

// Specs returns the Intune tool specifications, derived from the typed tools.
func (t *ToolSet) Specs(_ context.Context) ([]gollem.ToolSpec, error) {
	specs := make([]gollem.ToolSpec, len(t.tools))
	for i, tool := range t.tools {
		specs[i] = tool.Spec()
	}
	return specs, nil
}

// Run executes the named Intune tool by delegating to the matching typed tool.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	tool, ok := t.toolByName[name]
	if !ok {
		return nil, goerr.New("invalid function name", goerr.V("name", name))
	}
	return tool.Run(ctx, args)
}

// Ping verifies credentials by acquiring an OAuth access token.
func (t *ToolSet) Ping(ctx context.Context) error {
	if _, err := t.getToken(ctx); err != nil {
		return goerr.Wrap(err, "Intune ping failed: unable to acquire access token")
	}
	return nil
}

// sanitizeOData escapes single quotes in OData filter values to prevent injection.
func sanitizeOData(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func (t *ToolSet) searchDevicesByUser(ctx context.Context, upn string) (map[string]any, error) {
	filter := fmt.Sprintf("userPrincipalName eq '%s'", sanitizeOData(upn))
	devices, err := t.queryManagedDevices(ctx, filter)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to query managed devices by user", goerr.V("upn", upn))
	}

	signIns := t.fetchSignInLogs(ctx, fmt.Sprintf("userPrincipalName eq '%s'", sanitizeOData(upn)))

	return map[string]any{
		"devices":       devices,
		"signInHistory": signIns,
		"totalDevices":  len(devices),
	}, nil
}

func (t *ToolSet) searchDevicesByHostname(ctx context.Context, deviceName string) (map[string]any, error) {
	filter := fmt.Sprintf("deviceName eq '%s'", sanitizeOData(deviceName))
	devices, err := t.queryManagedDevices(ctx, filter)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to query managed devices by hostname",
			goerr.V("device_name", deviceName))
	}

	// Sign-in logs are keyed by user, so derive the UPN from the first matched
	// device (matches the legacy behavior).
	var signIns []any
	if len(devices) > 0 {
		if first, ok := devices[0].(map[string]any); ok {
			if upn, ok := first["userPrincipalName"].(string); ok && upn != "" {
				signIns = t.fetchSignInLogs(ctx, fmt.Sprintf("userPrincipalName eq '%s'", sanitizeOData(upn)))
			}
		}
	}

	return map[string]any{
		"devices":       devices,
		"signInHistory": signIns,
		"totalDevices":  len(devices),
	}, nil
}

// fetchSignInLogs retrieves recent Azure AD sign-in logs (IP history) for the
// given filter. It is best-effort: failures are logged and yield a nil history
// rather than failing the request, because the auditLogs/signIns endpoint needs
// additional Graph permissions that may not be granted.
func (t *ToolSet) fetchSignInLogs(ctx context.Context, filter string) []any {
	params := url.Values{
		"$filter":  {filter},
		"$top":     {"50"},
		"$orderby": {"createdDateTime desc"},
		"$select":  {"ipAddress,createdDateTime,clientAppUsed,deviceDetail"},
	}
	endpoint := fmt.Sprintf("%s/auditLogs/signIns?%s", t.baseURL, params.Encode())

	body, err := t.callGraphAPI(ctx, endpoint)
	if err != nil {
		t.logger.WarnContext(ctx, "failed to fetch sign-in logs (optional)", slog.Any("error", err))
		return nil
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.logger.WarnContext(ctx, "failed to unmarshal sign-in logs", slog.Any("error", err))
		return nil
	}

	values, ok := result["value"].([]any)
	if !ok {
		return nil
	}
	return values
}

// queryManagedDevices queries the Graph API for managed devices matching filter.
// On 401 responses it clears the cached token and retries once.
func (t *ToolSet) queryManagedDevices(ctx context.Context, filter string) ([]any, error) {
	params := url.Values{"$filter": {filter}}
	endpoint := fmt.Sprintf("%s/deviceManagement/managedDevices?%s", t.baseURL, params.Encode())

	body, err := t.callGraphAPI(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, goerr.Wrap(err, "failed to unmarshal managed devices response")
	}

	values, ok := result["value"].([]any)
	if !ok {
		return []any{}, nil
	}
	return values, nil
}

// callGraphAPI makes an authenticated GET request to the given Graph endpoint.
// On 401 it clears the token cache and retries once.
func (t *ToolSet) callGraphAPI(ctx context.Context, endpoint string) ([]byte, error) {
	body, statusCode, err := t.doGraphRequest(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	if statusCode == http.StatusUnauthorized {
		t.clearToken()
		body, statusCode, err = t.doGraphRequest(ctx, endpoint)
		if err != nil {
			return nil, err
		}
	}

	if statusCode != http.StatusOK {
		return nil, goerr.New("Graph API request failed",
			goerr.V("status_code", statusCode),
			goerr.V("body", string(body)),
			goerr.V("endpoint", endpoint))
	}

	return body, nil
}

func (t *ToolSet) doGraphRequest(ctx context.Context, endpoint string) ([]byte, int, error) {
	token, err := t.getToken(ctx)
	if err != nil {
		return nil, 0, goerr.Wrap(err, "failed to acquire access token")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, goerr.Wrap(err, "failed to create Graph request", goerr.V("endpoint", endpoint))
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, 0, goerr.Wrap(err, "failed to send Graph request", goerr.V("endpoint", endpoint))
	}
	defer safeClose(t.logger, resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, goerr.Wrap(err, "failed to read Graph response body")
	}

	return body, resp.StatusCode, nil
}

// getToken returns a cached access token, fetching a new one when the cache is
// empty or the token is close to expiry. Safe for concurrent use.
func (t *ToolSet) getToken(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.accessToken != "" && time.Now().Before(t.tokenExpiry.Add(-tokenExpiryBuffer)) {
		return t.accessToken, nil
	}

	return t.fetchToken(ctx)
}

// fetchToken requests a new access token using Client Credentials Flow.
// Must be called with t.mu held.
func (t *ToolSet) fetchToken(ctx context.Context) (string, error) {
	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {t.clientID},
		"client_secret": {t.clientSecret},
		"scope":         {graphDefaultScope},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.tokenEndpoint,
		strings.NewReader(data.Encode()))
	if err != nil {
		return "", goerr.Wrap(err, "failed to create token request",
			goerr.V("token_endpoint", t.tokenEndpoint))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", goerr.Wrap(err, "failed to send token request",
			goerr.V("token_endpoint", t.tokenEndpoint))
	}
	defer safeClose(t.logger, resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", goerr.Wrap(err, "failed to read token response body")
	}

	if resp.StatusCode != http.StatusOK {
		return "", goerr.New("failed to obtain access token",
			goerr.V("status_code", resp.StatusCode),
			goerr.V("body", string(body)))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", goerr.Wrap(err, "failed to unmarshal token response",
			goerr.V("body", string(body)))
	}

	t.accessToken = tokenResp.AccessToken
	t.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	return t.accessToken, nil
}

// clearToken evicts the cached access token. Used on 401 responses.
func (t *ToolSet) clearToken() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.accessToken = ""
	t.tokenExpiry = time.Time{}
}
