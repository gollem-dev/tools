package falcon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/m-mizutani/goerr/v2"
)

// tokenProvider manages the OAuth2 Client Credentials Flow for the CrowdStrike
// Falcon API. It acquires a bearer token and refreshes it before expiry. A
// single provider is owned by one ToolSet instance; its cache is guarded by a
// mutex and never shared across the package.
type tokenProvider struct {
	clientID     string
	clientSecret string
	baseURL      string
	httpClient   *http.Client
	logger       *slog.Logger

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// tokenResponse represents the OAuth2 token response from CrowdStrike.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

// newTokenProvider creates a tokenProvider. The httpClient and logger are
// injected so tests can drive it against an httptest server.
func newTokenProvider(clientID, clientSecret, baseURL string, httpClient *http.Client, logger *slog.Logger) *tokenProvider {
	return &tokenProvider{
		clientID:     clientID,
		clientSecret: clientSecret,
		baseURL:      baseURL,
		httpClient:   httpClient,
		logger:       logger,
	}
}

// getToken returns a valid bearer token, refreshing if necessary.
func (tp *tokenProvider) getToken(ctx context.Context) (string, error) {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	// Return the cached token if it is still valid with a 30-second buffer.
	if tp.token != "" && time.Now().Add(30*time.Second).Before(tp.expiry) {
		return tp.token, nil
	}

	if err := tp.refreshToken(ctx); err != nil {
		return "", err
	}

	return tp.token, nil
}

// clearToken invalidates the cached token, forcing a refresh on the next
// getToken call. Used after a 401 to recover from a server-side revocation.
func (tp *tokenProvider) clearToken() {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.token = ""
	tp.expiry = time.Time{}
}

// refreshToken acquires a new OAuth2 token from the CrowdStrike API. The caller
// must hold tp.mu.
func (tp *tokenProvider) refreshToken(ctx context.Context) error {
	tp.logger.Debug("refreshing CrowdStrike OAuth2 token",
		slog.String("base_url", tp.baseURL),
		slog.String("client_id", tp.clientID),
	)

	form := url.Values{
		"client_id":     {tp.clientID},
		"client_secret": {tp.clientSecret},
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		tp.baseURL+"/oauth2/token",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return goerr.Wrap(err, "failed to create token request")
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := tp.httpClient.Do(req)
	if err != nil {
		return goerr.Wrap(err, "failed to send token request")
	}
	defer safeClose(tp.logger, resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return goerr.Wrap(err, "failed to read token response body")
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		tp.logger.Warn("CrowdStrike OAuth2 token request failed", slog.Int("status", resp.StatusCode))
		return goerr.New("OAuth2 token request failed",
			goerr.V("status", resp.StatusCode),
			goerr.V("body", string(body)),
		)
	}

	var tokenResp tokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return goerr.Wrap(err, "failed to parse token response")
	}

	tp.token = tokenResp.AccessToken
	tp.expiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	tp.logger.Debug("CrowdStrike OAuth2 token refreshed", slog.Int("expires_in", tokenResp.ExpiresIn))

	return nil
}
