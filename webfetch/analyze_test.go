package webfetch_test

import (
	"context"
	"os"
	"testing"

	// The gemini import is intentionally restricted to this test file so that
	// the library code itself remains free of LLM provider dependencies.
	"github.com/gollem-dev/gollem/llm/gemini"
	"github.com/gollem-dev/tools/webfetch"
	"github.com/m-mizutani/gt"
)

// TestLiveWithLLM fetches a real URL and runs the LLM analysis pipeline using
// Google Vertex AI Gemini. It requires both TEST_WEBFETCH_URL and
// TEST_GEMINI_PROJECT_ID to be set; if either is absent the test is skipped.
//
// Optional env vars:
//   - TEST_GEMINI_LOCATION  — Vertex AI region (default "global")
func TestLiveWithLLM(t *testing.T) {
	targetURL, ok := os.LookupEnv("TEST_WEBFETCH_URL")
	if !ok {
		t.Skip("TEST_WEBFETCH_URL is not set")
	}

	projectID, ok := os.LookupEnv("TEST_GEMINI_PROJECT_ID")
	if !ok {
		t.Skip("TEST_GEMINI_PROJECT_ID is not set")
	}

	location := "global"
	if loc, ok := os.LookupEnv("TEST_GEMINI_LOCATION"); ok {
		location = loc
	}

	ctx := context.Background()

	llmClient := gt.R1(gemini.New(ctx, projectID, location)).NoError(t)

	ts := gt.R1(webfetch.New(
		webfetch.WithLLMClient(llmClient),
	)).NoError(t)

	// Ping verifies that the Gemini client is operational.
	gt.NoError(t, ts.Ping(ctx)).Required()

	result := gt.R1(ts.Run(ctx, "web_fetch", map[string]any{
		"url": targetURL,
	})).NoError(t)

	gt.Map(t, result).HasKey("result")
	gt.Map(t, result).HasKey("url")
	gt.Map(t, result).HasKey("status")

	// When LLM is enabled and the page is clean, llm_analysis must NOT be set.
	_, hasAnalysis := result["llm_analysis"]
	gt.Bool(t, hasAnalysis).False()

	resultText, ok := result["result"].(string)
	gt.Bool(t, ok).True()
	gt.Bool(t, len(resultText) > 0).True()
}
