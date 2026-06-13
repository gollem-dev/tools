package github_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/m-mizutani/gt"
)

const blameResponseJSON = `{
  "data": {
    "repository": {
      "object": {
        "blame": {
          "ranges": [
            {
              "startingLine": 1,
              "endingLine": 5,
              "commit": {
                "oid": "abc123def456",
                "message": "Add main function",
                "author": {
                  "name": "octocat",
                  "date": "2024-01-01T00:00:00Z"
                }
              }
            }
          ]
        }
      }
    }
  }
}`

func TestRunGetBlame(t *testing.T) {
	fake := &fakeClient{
		doGraphQLFn: func(_ context.Context, req *http.Request) (*http.Response, error) {
			gt.String(t, req.URL.String()).Equal("https://api.github.com/graphql")
			gt.String(t, req.Method).Equal(http.MethodPost)

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(blameResponseJSON)),
				Header:     make(http.Header),
			}, nil
		},
	}

	ts := newFakeToolSet(t, fake)
	result := gt.R1(ts.Run(context.Background(), "github_get_blame", map[string]any{
		"owner": "octocat",
		"repo":  "Hello-World",
		"path":  "main.go",
		"ref":   "main",
	})).NoError(t)

	gt.Map(t, result).HasKeyValue("repository", "octocat/Hello-World")
	gt.Map(t, result).HasKeyValue("path", "main.go")
	gt.Map(t, result).HasKeyValue("ref", "main")
	gt.V(t, result["count"]).Equal(1)
}

func TestRunGetBlameMissingOwner(t *testing.T) {
	fake := &fakeClient{}
	ts := newFakeToolSet(t, fake)

	_, err := ts.Run(context.Background(), "github_get_blame", map[string]any{
		"repo": "Hello-World",
		"path": "main.go",
	})
	gt.Error(t, err).Contains("owner is required")
}

func TestRunGetBlameMissingRepo(t *testing.T) {
	fake := &fakeClient{}
	ts := newFakeToolSet(t, fake)

	_, err := ts.Run(context.Background(), "github_get_blame", map[string]any{
		"owner": "octocat",
		"path":  "main.go",
	})
	gt.Error(t, err).Contains("repo is required")
}

func TestRunGetBlameMissingPath(t *testing.T) {
	fake := &fakeClient{}
	ts := newFakeToolSet(t, fake)

	_, err := ts.Run(context.Background(), "github_get_blame", map[string]any{
		"owner": "octocat",
		"repo":  "Hello-World",
	})
	gt.Error(t, err).Contains("path is required")
}

func TestRunGetBlameHTTPError(t *testing.T) {
	fake := &fakeClient{
		doGraphQLFn: func(_ context.Context, _ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Body:       io.NopCloser(strings.NewReader(`{"message":"Bad credentials"}`)),
				Header:     make(http.Header),
			}, nil
		},
	}

	ts := newFakeToolSet(t, fake)
	_, err := ts.Run(context.Background(), "github_get_blame", map[string]any{
		"owner": "octocat",
		"repo":  "Hello-World",
		"path":  "main.go",
	})
	gt.Error(t, err).Contains("GraphQL request failed")
}

func TestRunGetBlameGraphQLErrors(t *testing.T) {
	body := `{"errors":[{"message":"Could not resolve to a Repository"}]}`
	fake := &fakeClient{
		doGraphQLFn: func(_ context.Context, _ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		},
	}

	ts := newFakeToolSet(t, fake)
	_, err := ts.Run(context.Background(), "github_get_blame", map[string]any{
		"owner": "octocat",
		"repo":  "nonexistent",
		"path":  "main.go",
	})
	gt.Error(t, err).Contains("GraphQL errors")
}

func TestRunGetBlameDefaultRef(t *testing.T) {
	var capturedBody string
	fake := &fakeClient{
		doGraphQLFn: func(_ context.Context, req *http.Request) (*http.Response, error) {
			bodyBytes, _ := io.ReadAll(req.Body)
			capturedBody = string(bodyBytes)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(blameResponseJSON)),
				Header:     make(http.Header),
			}, nil
		},
	}

	ts := newFakeToolSet(t, fake)
	// No "ref" provided — should default to "HEAD" so GitHub resolves the
	// repository's default branch (works for main, master, or any other).
	result := gt.R1(ts.Run(context.Background(), "github_get_blame", map[string]any{
		"owner": "octocat",
		"repo":  "Hello-World",
		"path":  "main.go",
	})).NoError(t)

	// The default ref must be "HEAD", not a hard-coded "main", and the path is
	// passed as a separate GraphQL variable (not combined into "ref:path").
	gt.String(t, capturedBody).Contains(`"ref":"HEAD"`)
	gt.String(t, capturedBody).Contains(`"path":"main.go"`)
	gt.Map(t, result).HasKeyValue("ref", "HEAD")
}
