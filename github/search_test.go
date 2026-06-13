package github_test

import (
	"context"
	"net/http"
	"testing"

	ghlib "github.com/google/go-github/v74/github"
	"github.com/m-mizutani/gt"
)

// ---------------------------------------------------------------------------
// github_code_search
// ---------------------------------------------------------------------------

func TestRunCodeSearch(t *testing.T) {
	var capturedQuery string
	fake := &fakeClient{
		searchCodeFn: func(_ context.Context, query string, _ *ghlib.SearchOptions) (*ghlib.CodeSearchResult, *ghlib.Response, error) {
			capturedQuery = query
			fullName := "octocat/Hello-World"
			path := "main.go"
			htmlURL := "https://github.com/octocat/Hello-World/blob/main/main.go"
			fragment := "func main()"
			total := 1
			return &ghlib.CodeSearchResult{
				Total: &total,
				CodeResults: []*ghlib.CodeResult{
					{
						Repository: &ghlib.Repository{FullName: &fullName},
						Path:       &path,
						HTMLURL:    &htmlURL,
						TextMatches: []*ghlib.TextMatch{
							{Fragment: &fragment},
						},
					},
				},
			}, nil, nil
		},
	}

	ts := newFakeToolSet(t, fake)
	result := gt.R1(ts.Run(context.Background(), "github_code_search", map[string]any{
		"query":    "func main",
		"language": "go",
	})).NoError(t)

	gt.String(t, capturedQuery).Contains("func main").Contains("language:go")
	gt.Map(t, result).HasKey("results")
	gt.Map(t, result).HasKey("total")
	gt.V(t, result["total"]).Equal(1)
}

func TestRunCodeSearchMissingQuery(t *testing.T) {
	fake := &fakeClient{}
	ts := newFakeToolSet(t, fake)

	_, err := ts.Run(context.Background(), "github_code_search", map[string]any{})
	gt.Error(t, err).Contains("query is required")
}

func TestRunCodeSearchRepoFilter(t *testing.T) {
	var capturedQuery string
	fake := &fakeClient{
		searchCodeFn: func(_ context.Context, query string, _ *ghlib.SearchOptions) (*ghlib.CodeSearchResult, *ghlib.Response, error) {
			capturedQuery = query
			total := 0
			return &ghlib.CodeSearchResult{Total: &total}, nil, nil
		},
	}

	ts := newFakeToolSet(t, fake)
	gt.R1(ts.Run(context.Background(), "github_code_search", map[string]any{
		"query":       "test",
		"repo_filter": "owner/repo1,owner/repo2",
	})).NoError(t)

	gt.String(t, capturedQuery).Contains("repo:owner/repo1").Contains("repo:owner/repo2")
}

// ---------------------------------------------------------------------------
// github_issue_search
// ---------------------------------------------------------------------------

func TestRunIssueSearch(t *testing.T) {
	var capturedQuery string
	fake := &fakeClient{
		searchIssuesFn: func(_ context.Context, query string, _ *ghlib.SearchOptions) (*ghlib.IssuesSearchResult, *ghlib.Response, error) {
			capturedQuery = query
			num := 42
			title := "Fix the bug"
			state := "open"
			htmlURL := "https://github.com/owner/repo/issues/42"
			repoURL := "https://api.github.com/repos/owner/repo"
			total := 1
			return &ghlib.IssuesSearchResult{
				Total: &total,
				Issues: []*ghlib.Issue{
					{
						Number:        &num,
						Title:         &title,
						State:         &state,
						HTMLURL:       &htmlURL,
						RepositoryURL: &repoURL,
					},
				},
			}, nil, nil
		},
	}

	ts := newFakeToolSet(t, fake)
	result := gt.R1(ts.Run(context.Background(), "github_issue_search", map[string]any{
		"query": "Fix the bug",
		"state": "open",
	})).NoError(t)

	gt.String(t, capturedQuery).Contains("Fix the bug").Contains("state:open")
	gt.Map(t, result).HasKey("results")
	gt.V(t, result["total"]).Equal(1)
}

func TestRunIssueSearchMissingQuery(t *testing.T) {
	fake := &fakeClient{}
	ts := newFakeToolSet(t, fake)

	_, err := ts.Run(context.Background(), "github_issue_search", map[string]any{})
	gt.Error(t, err).Contains("query is required")
}

func TestRunIssueSearchTypeFilter(t *testing.T) {
	var capturedQuery string
	fake := &fakeClient{
		searchIssuesFn: func(_ context.Context, query string, _ *ghlib.SearchOptions) (*ghlib.IssuesSearchResult, *ghlib.Response, error) {
			capturedQuery = query
			total := 0
			return &ghlib.IssuesSearchResult{Total: &total}, nil, nil
		},
	}

	ts := newFakeToolSet(t, fake)
	gt.R1(ts.Run(context.Background(), "github_issue_search", map[string]any{
		"query": "test",
		"type":  "pr",
	})).NoError(t)

	gt.String(t, capturedQuery).Contains("type:pr")
}

// ---------------------------------------------------------------------------
// Error propagation
// ---------------------------------------------------------------------------

func TestRunCodeSearchError(t *testing.T) {
	fake := &fakeClient{
		searchCodeFn: func(_ context.Context, _ string, _ *ghlib.SearchOptions) (*ghlib.CodeSearchResult, *ghlib.Response, error) {
			return nil, nil, &ghlib.ErrorResponse{
				Response: &http.Response{StatusCode: http.StatusForbidden},
				Message:  "forbidden",
			}
		},
	}

	ts := newFakeToolSet(t, fake)
	_, err := ts.Run(context.Background(), "github_code_search", map[string]any{"query": "test"})
	gt.Error(t, err).Contains("failed to search code")
}
