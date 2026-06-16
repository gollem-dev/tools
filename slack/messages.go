package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"golang.org/x/sync/errgroup"
)

const (
	// maxGetMessagesTargets bounds how many message references one call may
	// fetch, keeping fan-out (and Slack rate-limit pressure) in check.
	maxGetMessagesTargets = 10

	// defaultThreadLimit / maxThreadLimit bound the replies fetched per thread.
	// Slack reduced conversations.replies' default and maximum limit to 15 for
	// apps newly distributed outside the Marketplace as of 2025-05-29, so the
	// default is kept at 15 to work on both old (max 1000) and new tiers. The
	// ceiling is left higher for apps still on the legacy tier; callers that
	// request more than their tier allows get a visible per-target error.
	defaultThreadLimit = 15
	maxThreadLimit     = 200
)

// messageTarget identifies one message to fetch by channel and timestamp.
type messageTarget struct {
	ChannelID string
	TS        string
}

// runGetMessages fetches one or more Slack messages (and optionally their
// thread context) in parallel. Per-target failures are reported in the response
// without aborting the whole call; the call fails only when every target fails.
func (t *ToolSet) runGetMessages(ctx context.Context, args map[string]any) (map[string]any, error) {
	rawTargets, ok := args["targets"].([]any)
	if !ok || len(rawTargets) == 0 {
		return nil, goerr.New("targets is required and must be a non-empty array", goerr.V("args", args))
	}
	if len(rawTargets) > maxGetMessagesTargets {
		return nil, goerr.New("too many targets",
			goerr.V("count", len(rawTargets)), goerr.V("max", maxGetMessagesTargets))
	}

	targets := make([]messageTarget, 0, len(rawTargets))
	for i, rt := range rawTargets {
		obj, ok := rt.(map[string]any)
		if !ok {
			return nil, goerr.New("each target must be an object", goerr.V("index", i))
		}
		channelID, _ := obj["channel_id"].(string)
		ts, _ := obj["ts"].(string)
		if channelID == "" || ts == "" {
			return nil, goerr.New("each target requires channel_id and ts",
				goerr.V("index", i), goerr.V("target", obj))
		}
		targets = append(targets, messageTarget{ChannelID: channelID, TS: ts})
	}

	includeThread := true
	if v, ok := args["include_thread"].(bool); ok {
		includeThread = v
	}

	threadLimit := defaultThreadLimit
	if v, ok := args["thread_limit"].(float64); ok && v > 0 {
		threadLimit = int(v)
	}
	if threadLimit > maxThreadLimit {
		threadLimit = maxThreadLimit
	}
	// When the thread is not wanted, fetch only the message itself.
	limit := threadLimit
	if !includeThread {
		limit = 1
	}

	results := make([]map[string]any, len(targets))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(len(targets))
	for i, target := range targets {
		g.Go(func() error {
			// Per-target failures are captured in the result, not propagated, so
			// one bad target does not abort the others. The goroutine therefore
			// never returns a non-nil error.
			results[i] = t.fetchOneTarget(gctx, target, limit)
			return nil
		})
	}
	// Wait never returns an error because the goroutines above always return nil.
	_ = g.Wait()

	allFailed := true
	for _, r := range results {
		if _, hasErr := r["error"]; !hasErr {
			allFailed = false
			break
		}
	}
	if allFailed {
		return nil, goerr.New("all Slack message targets failed", goerr.V("count", len(targets)))
	}

	out := make([]any, len(results))
	for i, r := range results {
		out[i] = r
	}
	return map[string]any{"results": out}, nil
}

// fetchOneTarget retrieves a single message (and its thread up to limit) plus
// permalink. On failure it returns a result carrying an "error" entry rather
// than propagating, so the caller can report partial success.
func (t *ToolSet) fetchOneTarget(ctx context.Context, target messageTarget, limit int) map[string]any {
	base := map[string]any{
		"channel_id": target.ChannelID,
		"ts":         target.TS,
	}

	replies, err := t.conversationsReplies(ctx, target.ChannelID, target.TS, limit)
	if err != nil {
		base["error"] = err.Error()
		return base
	}

	msgs := make([]any, 0, len(replies))
	for _, m := range replies {
		msgs = append(msgs, map[string]any{
			"user_id":   m.User,
			"username":  m.Username,
			"text":      m.Text,
			"ts":        m.Timestamp,
			"thread_ts": m.ThreadTS,
		})
	}
	base["messages"] = msgs

	// Permalink is auxiliary: if it fails, keep the messages and log instead of
	// failing the whole target.
	permalink, err := t.getPermalink(ctx, target.ChannelID, target.TS)
	if err != nil {
		t.logger.WarnContext(ctx, "failed to fetch Slack permalink",
			slog.String("channel_id", target.ChannelID), slog.String("ts", target.TS),
			slog.Any("error", err))
	} else {
		base["permalink"] = permalink
	}

	return base
}

// replyMessage mirrors a single message from conversations.replies. Slack only
// returns the user ID for human messages; username is populated for bot/app
// messages, so it is best-effort here.
type replyMessage struct {
	User      string `json:"user"`
	Username  string `json:"username"`
	Text      string `json:"text"`
	Timestamp string `json:"ts"`
	ThreadTS  string `json:"thread_ts"`
}

type repliesResponse struct {
	OK       bool           `json:"ok"`
	Error    string         `json:"error,omitempty"`
	Messages []replyMessage `json:"messages"`
}

// conversationsReplies fetches up to limit messages of a thread rooted at ts.
// A user token can read public channels even when no bot has joined them.
func (t *ToolSet) conversationsReplies(ctx context.Context, channelID, ts string, limit int) ([]replyMessage, error) {
	params := url.Values{}
	params.Set("channel", channelID)
	params.Set("ts", ts)
	params.Set("limit", strconv.Itoa(limit))
	endpoint := fmt.Sprintf("%s/conversations.replies?%s", t.baseURL, params.Encode())

	body, err := t.slackGet(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	var parsed repliesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, goerr.Wrap(err, "failed to unmarshal conversations.replies response",
			goerr.V("body", string(body)))
	}
	if !parsed.OK {
		return nil, goerr.New("Slack conversations.replies error", goerr.V("error", parsed.Error))
	}
	return parsed.Messages, nil
}

type permalinkResponse struct {
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Permalink string `json:"permalink"`
}

// getPermalink resolves the permalink for a single message.
func (t *ToolSet) getPermalink(ctx context.Context, channelID, ts string) (string, error) {
	params := url.Values{}
	params.Set("channel", channelID)
	params.Set("message_ts", ts)
	endpoint := fmt.Sprintf("%s/chat.getPermalink?%s", t.baseURL, params.Encode())

	body, err := t.slackGet(ctx, endpoint)
	if err != nil {
		return "", err
	}

	var parsed permalinkResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", goerr.Wrap(err, "failed to unmarshal chat.getPermalink response",
			goerr.V("body", string(body)))
	}
	if !parsed.OK {
		return "", goerr.New("Slack chat.getPermalink error", goerr.V("error", parsed.Error))
	}
	return parsed.Permalink, nil
}

// slackGet issues a GET against a Slack web API endpoint, retrying on rate
// limiting (HTTP 429 / "rate_limited") with Retry-After backoff, and returns the
// raw response body. It mirrors searchMessages' retry discipline so all read
// paths behave consistently.
func (t *ToolSet) slackGet(ctx context.Context, endpoint string) ([]byte, error) {
	var lastErr error
	for attempt := range maxSearchRetries {
		if err := ctx.Err(); err != nil {
			return nil, goerr.Wrap(err, "context cancelled during Slack request")
		}

		body, retryAfter, retry, err := t.slackGetOnce(ctx, endpoint)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
		if attempt == maxSearchRetries-1 {
			break
		}
		wait := retryAfter
		if wait <= 0 {
			wait = t.retryWait * time.Duration(attempt+1)
		}
		t.logger.InfoContext(ctx, "Slack request rate limited; retrying",
			slog.Duration("wait", wait), slog.Int("attempt", attempt+1))
		if waitErr := sleepCtx(ctx, wait); waitErr != nil {
			return nil, goerr.Wrap(waitErr, "context cancelled while waiting to retry Slack request")
		}
	}
	return nil, goerr.Wrap(lastErr, "Slack request failed after retries",
		goerr.V("retries", maxSearchRetries))
}

// slackGetOnce performs a single GET and reports whether the caller should
// retry. The "rate_limited" API-level envelope error is detected so it can be
// retried like an HTTP 429.
func (t *ToolSet) slackGetOnce(ctx context.Context, endpoint string) (body []byte, retryAfter time.Duration, retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, false, goerr.Wrap(err, "failed to create Slack request", goerr.V("url", endpoint))
	}
	req.Header.Set("Authorization", "Bearer "+t.userToken)

	resp, err := t.client.Do(req)
	if err != nil {
		// Treat transport errors as retryable.
		return nil, 0, true, goerr.Wrap(err, "failed to send Slack request", goerr.V("url", endpoint))
	}
	defer safeClose(t.logger, resp.Body)

	eb := goerr.NewBuilder(goerr.V("status", resp.StatusCode), goerr.V("url", endpoint))

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, true, eb.Wrap(err, "failed to read Slack response body")
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, parseRetryAfter(resp), true, eb.New("Slack request rate limited (HTTP 429)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, false, eb.New("Slack request failed", goerr.V("body", string(b)))
	}

	// Peek at the shared {ok,error} envelope only to retry on rate_limited.
	var env struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(b, &env); err == nil && !env.OK && env.Error == "rate_limited" {
		return nil, 0, true, eb.New("Slack API rate limited")
	}

	return b, 0, false, nil
}
