package github_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/gollem-dev/tools/github"
	ghlib "github.com/google/go-github/v74/github"
	"github.com/m-mizutani/gt"
)

// fakeClient is a test double for ghClient. Each field is a function that
// can be set per test to control what the fake returns.
type fakeClient struct {
	searchCodeFn   func(ctx context.Context, query string, opts *ghlib.SearchOptions) (*ghlib.CodeSearchResult, *ghlib.Response, error)
	searchIssuesFn func(ctx context.Context, query string, opts *ghlib.SearchOptions) (*ghlib.IssuesSearchResult, *ghlib.Response, error)
	getContentsFn  func(ctx context.Context, owner, repo, path string, opts *ghlib.RepositoryContentGetOptions) (*ghlib.RepositoryContent, []*ghlib.RepositoryContent, *ghlib.Response, error)
	listCommitsFn  func(ctx context.Context, owner, repo string, opts *ghlib.CommitsListOptions) ([]*ghlib.RepositoryCommit, *ghlib.Response, error)
	doGraphQLFn    func(ctx context.Context, req *http.Request) (*http.Response, error)
}

func (f *fakeClient) SearchCode(ctx context.Context, query string, opts *ghlib.SearchOptions) (*ghlib.CodeSearchResult, *ghlib.Response, error) {
	return f.searchCodeFn(ctx, query, opts)
}

func (f *fakeClient) SearchIssues(ctx context.Context, query string, opts *ghlib.SearchOptions) (*ghlib.IssuesSearchResult, *ghlib.Response, error) {
	return f.searchIssuesFn(ctx, query, opts)
}

func (f *fakeClient) GetContents(ctx context.Context, owner, repo, path string, opts *ghlib.RepositoryContentGetOptions) (*ghlib.RepositoryContent, []*ghlib.RepositoryContent, *ghlib.Response, error) {
	return f.getContentsFn(ctx, owner, repo, path, opts)
}

func (f *fakeClient) ListCommits(ctx context.Context, owner, repo string, opts *ghlib.CommitsListOptions) ([]*ghlib.RepositoryCommit, *ghlib.Response, error) {
	return f.listCommitsFn(ctx, owner, repo, opts)
}

func (f *fakeClient) DoGraphQL(ctx context.Context, req *http.Request) (*http.Response, error) {
	return f.doGraphQLFn(ctx, req)
}

// newFakeToolSet returns a ToolSet with a fake client pre-installed.
// When WithClientForTest is applied first, New skips ghinstallation.New so the
// PEM is never parsed — any non-empty string satisfies the required-field check.
const testPEM = "placeholder-private-key-for-tests-only"

func newFakeToolSet(t *testing.T, fake *fakeClient) *github.ToolSet {
	t.Helper()
	ts := gt.R1(github.New(
		12345,
		67890,
		testPEM,
		github.WithClientForTest(fake),
	)).NoError(t)
	return ts
}

// ---------------------------------------------------------------------------
// New — validation
// ---------------------------------------------------------------------------

func TestNewMissingAppID(t *testing.T) {
	_, err := github.New(0, 1, testPEM)
	gt.Error(t, err).Contains("App ID")
}

func TestNewMissingInstallationID(t *testing.T) {
	_, err := github.New(1, 0, testPEM)
	gt.Error(t, err).Contains("installation ID")
}

func TestNewMissingPrivateKey(t *testing.T) {
	_, err := github.New(1, 1, "")
	gt.Error(t, err).Contains("private key")
}

func TestNewInvalidPEM(t *testing.T) {
	_, err := github.New(1, 1, "not-a-pem")
	gt.Error(t, err)
}

// ---------------------------------------------------------------------------
// Specs
// ---------------------------------------------------------------------------

func TestSpecs(t *testing.T) {
	fake := &fakeClient{}
	ts := newFakeToolSet(t, fake)

	specs := gt.R1(ts.Specs(context.Background())).NoError(t)
	gt.Array(t, specs).Length(5)

	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
	}
	gt.Array(t, names).
		Has("github_code_search").
		Has("github_issue_search").
		Has("github_get_content").
		Has("github_list_commits").
		Has("github_get_blame")

	// Verify required parameters exist.
	specsMap := make(map[string]map[string]bool)
	for _, s := range specs {
		required := make(map[string]bool)
		for name, p := range s.Parameters {
			if p.Required {
				required[name] = true
			}
		}
		specsMap[s.Name] = required
	}
	gt.Map(t, specsMap["github_code_search"]).HasKey("query")
	gt.Map(t, specsMap["github_issue_search"]).HasKey("query")
	gt.Map(t, specsMap["github_get_content"]).HasKey("owner")
	gt.Map(t, specsMap["github_get_content"]).HasKey("repo")
	gt.Map(t, specsMap["github_get_content"]).HasKey("path")
	gt.Map(t, specsMap["github_list_commits"]).HasKey("owner")
	gt.Map(t, specsMap["github_list_commits"]).HasKey("repo")
	gt.Map(t, specsMap["github_get_blame"]).HasKey("owner")
	gt.Map(t, specsMap["github_get_blame"]).HasKey("repo")
	gt.Map(t, specsMap["github_get_blame"]).HasKey("path")
}

// ---------------------------------------------------------------------------
// Run — unknown name
// ---------------------------------------------------------------------------

func TestRunUnknownName(t *testing.T) {
	fake := &fakeClient{}
	ts := newFakeToolSet(t, fake)

	_, err := ts.Run(context.Background(), "github_nonexistent", map[string]any{})
	gt.Error(t, err).Contains("unknown tool name")
}

// ---------------------------------------------------------------------------
// Ping with fake client
// ---------------------------------------------------------------------------

func TestPingFake(t *testing.T) {
	fake := &fakeClient{
		listCommitsFn: func(_ context.Context, _, _ string, _ *ghlib.CommitsListOptions) ([]*ghlib.RepositoryCommit, *ghlib.Response, error) {
			return []*ghlib.RepositoryCommit{}, nil, nil
		},
	}
	ts := newFakeToolSet(t, fake)
	gt.NoError(t, ts.Ping(context.Background()))
}

// TestLive hits the real GitHub API. It runs only when all required
// environment variables are set:
//
//   - TEST_GITHUB_APP_ID              — GitHub App ID (numeric)
//   - TEST_GITHUB_APP_INSTALLATION_ID — GitHub App installation ID (numeric)
//   - TEST_GITHUB_APP_PRIVATE_KEY     — GitHub App private key in PEM format
//   - TEST_GITHUB_REPO                — target repository in "owner/repo" format
func TestLive(t *testing.T) {
	appIDStr, ok := os.LookupEnv("TEST_GITHUB_APP_ID")
	if !ok {
		t.Skip("TEST_GITHUB_APP_ID is not set")
	}
	installationIDStr, ok := os.LookupEnv("TEST_GITHUB_APP_INSTALLATION_ID")
	if !ok {
		t.Skip("TEST_GITHUB_APP_INSTALLATION_ID is not set")
	}
	privateKey, ok := os.LookupEnv("TEST_GITHUB_APP_PRIVATE_KEY")
	if !ok {
		t.Skip("TEST_GITHUB_APP_PRIVATE_KEY is not set")
	}
	repoFull, ok := os.LookupEnv("TEST_GITHUB_REPO")
	if !ok {
		t.Skip("TEST_GITHUB_REPO is not set")
	}

	appID, err := strconv.ParseInt(appIDStr, 10, 64)
	gt.NoError(t, err).Required()

	installationID, err := strconv.ParseInt(installationIDStr, 10, 64)
	gt.NoError(t, err).Required()

	parts := strings.SplitN(repoFull, "/", 2)
	gt.Array(t, parts).Length(2)
	owner, repo := parts[0], parts[1]

	ts := gt.R1(github.New(appID, installationID, privateKey)).NoError(t)

	// Verify connectivity.
	gt.NoError(t, ts.Ping(context.Background())).Required()

	t.Run("github_code_search", func(t *testing.T) {
		// Scope the search to the target repo to avoid rate-limit and noise.
		result := gt.R1(ts.Run(context.Background(), "github_code_search", map[string]any{
			"query":       "func",
			"repo_filter": repoFull,
		})).NoError(t)

		gt.Map(t, result).HasKey("results")
		gt.Map(t, result).HasKey("total")

		// GitHub search is eventually consistent; assert the result is
		// JSON-marshalable rather than requiring non-empty matches.
		gt.R1(json.Marshal(result)).NoError(t)
	})

	t.Run("github_issue_search", func(t *testing.T) {
		result := gt.R1(ts.Run(context.Background(), "github_issue_search", map[string]any{
			"query": "repo:" + repoFull + " is:issue",
		})).NoError(t)

		gt.Map(t, result).HasKey("results")
		gt.Map(t, result).HasKey("total")

		gt.R1(json.Marshal(result)).NoError(t)
	})

	t.Run("github_get_content", func(t *testing.T) {
		result, err := ts.Run(context.Background(), "github_get_content", map[string]any{
			"owner": owner,
			"repo":  repo,
			"path":  "README.md",
		})
		if err != nil {
			t.Logf("github_get_content: README.md not found or error: %v", err)
			return
		}
		gt.Map(t, result).HasKey("content")
		gt.Map(t, result).HasKey("path")
		gt.Map(t, result).HasKey("repository")
	})

	t.Run("github_list_commits", func(t *testing.T) {
		result := gt.R1(ts.Run(context.Background(), "github_list_commits", map[string]any{
			"owner":    owner,
			"repo":     repo,
			"per_page": float64(5),
		})).NoError(t)

		gt.Map(t, result).HasKey("commits")
		gt.Map(t, result).HasKey("count")
		gt.Map(t, result).HasKey("repository")
	})

	t.Run("github_get_blame", func(t *testing.T) {
		// ref defaults to "main" inside runGetBlame; omit it here so the
		// implementation's own default is exercised. If the repo uses a
		// different default branch the test logs the error but does not fail
		// the suite — the intent is coverage, not a hard dependency on branch
		// naming.
		result, err := ts.Run(context.Background(), "github_get_blame", map[string]any{
			"owner": owner,
			"repo":  repo,
			"path":  "README.md",
		})
		if err != nil {
			t.Logf("github_get_blame: skipping assertion due to error (repo may not use 'main' as default branch): %v", err)
			return
		}
		gt.Map(t, result).HasKey("ranges")
		gt.Map(t, result).HasKey("count")
		gt.Map(t, result).HasKey("repository")
		gt.Map(t, result).HasKey("path")
		gt.Map(t, result).HasKey("ref")
	})
}
