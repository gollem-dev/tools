// Package github provides a gollem.ToolSet for GitHub code/issue search,
// file content retrieval, commit history listing, and git blame.
package github

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/gollem-dev/gollem"
	ghlib "github.com/google/go-github/v74/github"
	"github.com/m-mizutani/goerr/v2"
)

// ghClient abstracts the GitHub API surface used by the five tools so that
// tests can inject a fake without touching the network.
type ghClient interface {
	// SearchCode searches for code across GitHub.
	SearchCode(ctx context.Context, query string, opts *ghlib.SearchOptions) (*ghlib.CodeSearchResult, *ghlib.Response, error)

	// SearchIssues searches for issues and pull requests.
	SearchIssues(ctx context.Context, query string, opts *ghlib.SearchOptions) (*ghlib.IssuesSearchResult, *ghlib.Response, error)

	// GetContents returns the contents of a file or directory.
	GetContents(ctx context.Context, owner, repo, path string, opts *ghlib.RepositoryContentGetOptions) (*ghlib.RepositoryContent, []*ghlib.RepositoryContent, *ghlib.Response, error)

	// ListCommits lists commits for a repository.
	ListCommits(ctx context.Context, owner, repo string, opts *ghlib.CommitsListOptions) ([]*ghlib.RepositoryCommit, *ghlib.Response, error)

	// GetIssue returns a single issue by number. The returned issue also
	// represents a pull request when issue.IsPullRequest() is true.
	GetIssue(ctx context.Context, owner, repo string, number int) (*ghlib.Issue, *ghlib.Response, error)

	// ListIssueComments lists comments on an issue or pull request. Pull request
	// conversation comments are issue comments, so this serves both tools.
	ListIssueComments(ctx context.Context, owner, repo string, number int, opts *ghlib.IssueListCommentsOptions) ([]*ghlib.IssueComment, *ghlib.Response, error)

	// GetPullRequest returns a single pull request by number.
	GetPullRequest(ctx context.Context, owner, repo string, number int) (*ghlib.PullRequest, *ghlib.Response, error)

	// ListPullRequestReviews lists reviews on a pull request.
	ListPullRequestReviews(ctx context.Context, owner, repo string, number int, opts *ghlib.ListOptions) ([]*ghlib.PullRequestReview, *ghlib.Response, error)

	// ListPullRequestFiles lists the changed files of a pull request.
	ListPullRequestFiles(ctx context.Context, owner, repo string, number int, opts *ghlib.ListOptions) ([]*ghlib.CommitFile, *ghlib.Response, error)

	// DoGraphQL executes a raw HTTP request against the GitHub GraphQL endpoint.
	// The caller is responsible for marshalling the request body and
	// unmarshalling the response.
	DoGraphQL(ctx context.Context, req *http.Request) (*http.Response, error)
}

// defaultGHClient wraps a *ghlib.Client and the underlying http.Client (used
// for the GraphQL blame requests that go-github does not natively support).
type defaultGHClient struct {
	github     *ghlib.Client
	httpClient *http.Client
}

func (c *defaultGHClient) SearchCode(ctx context.Context, query string, opts *ghlib.SearchOptions) (*ghlib.CodeSearchResult, *ghlib.Response, error) {
	return c.github.Search.Code(ctx, query, opts)
}

func (c *defaultGHClient) SearchIssues(ctx context.Context, query string, opts *ghlib.SearchOptions) (*ghlib.IssuesSearchResult, *ghlib.Response, error) {
	return c.github.Search.Issues(ctx, query, opts)
}

func (c *defaultGHClient) GetContents(ctx context.Context, owner, repo, path string, opts *ghlib.RepositoryContentGetOptions) (*ghlib.RepositoryContent, []*ghlib.RepositoryContent, *ghlib.Response, error) {
	return c.github.Repositories.GetContents(ctx, owner, repo, path, opts)
}

func (c *defaultGHClient) ListCommits(ctx context.Context, owner, repo string, opts *ghlib.CommitsListOptions) ([]*ghlib.RepositoryCommit, *ghlib.Response, error) {
	return c.github.Repositories.ListCommits(ctx, owner, repo, opts)
}

func (c *defaultGHClient) GetIssue(ctx context.Context, owner, repo string, number int) (*ghlib.Issue, *ghlib.Response, error) {
	return c.github.Issues.Get(ctx, owner, repo, number)
}

func (c *defaultGHClient) ListIssueComments(ctx context.Context, owner, repo string, number int, opts *ghlib.IssueListCommentsOptions) ([]*ghlib.IssueComment, *ghlib.Response, error) {
	return c.github.Issues.ListComments(ctx, owner, repo, number, opts)
}

func (c *defaultGHClient) GetPullRequest(ctx context.Context, owner, repo string, number int) (*ghlib.PullRequest, *ghlib.Response, error) {
	return c.github.PullRequests.Get(ctx, owner, repo, number)
}

func (c *defaultGHClient) ListPullRequestReviews(ctx context.Context, owner, repo string, number int, opts *ghlib.ListOptions) ([]*ghlib.PullRequestReview, *ghlib.Response, error) {
	return c.github.PullRequests.ListReviews(ctx, owner, repo, number, opts)
}

func (c *defaultGHClient) ListPullRequestFiles(ctx context.Context, owner, repo string, number int, opts *ghlib.ListOptions) ([]*ghlib.CommitFile, *ghlib.Response, error) {
	return c.github.PullRequests.ListFiles(ctx, owner, repo, number, opts)
}

func (c *defaultGHClient) DoGraphQL(ctx context.Context, req *http.Request) (*http.Response, error) {
	return c.httpClient.Do(req)
}

// ToolSet implements gollem.ToolSet for GitHub. Fields are unexported;
// configure via Option.
type ToolSet struct {
	appID          int64
	installationID int64
	privateKey     string
	logger         *slog.Logger

	// client holds the ghClient instance. It is set during New from the real
	// ghinstallation transport, or replaced by tests via export_test.go.
	client    ghClient
	transport *ghinstallation.Transport

	// tools holds the typed tool definitions built at construction time.
	tools      []gollem.Tool
	toolByName map[string]gollem.Tool
}

// Startup assertions: a malformed input/output type (a broken struct tag, a
// non-object kind) is a programming error that should surface at init rather
// than on the first LLM call. See gollem docs "Validating Tool Types".
var (
	_ = gollem.MustToolSchema[codeSearchInput, map[string]any]()
	_ = gollem.MustToolSchema[issueSearchInput, map[string]any]()
	_ = gollem.MustToolSchema[getContentInput, map[string]any]()
	_ = gollem.MustToolSchema[listCommitsInput, map[string]any]()
	_ = gollem.MustToolSchema[getBlameInput, map[string]any]()
	_ = gollem.MustToolSchema[getIssueInput, map[string]any]()
	_ = gollem.MustToolSchema[getPullRequestInput, map[string]any]()
)

var _ gollem.ToolSet = (*ToolSet)(nil)

// Option configures a ToolSet.
type Option func(*ToolSet)

// WithLogger sets the logger. A nil logger keeps the default (slog.Default()).
func WithLogger(logger *slog.Logger) Option {
	return func(t *ToolSet) {
		if logger != nil {
			t.logger = logger
		}
	}
}

// New constructs the ToolSet with the three required credentials as positional
// arguments. appID and installationID must be non-zero; privateKey must be a
// non-empty PEM string. Optional settings (e.g. WithLogger) are passed as opts.
// New performs only in-memory validation and transport construction; use Ping to
// verify connectivity.
func New(appID int64, installationID int64, privateKey string, opts ...Option) (*ToolSet, error) {
	if appID == 0 {
		return nil, goerr.New("GitHub App ID is required")
	}
	if installationID == 0 {
		return nil, goerr.New("GitHub App installation ID is required")
	}
	if privateKey == "" {
		return nil, goerr.New("GitHub App private key is required")
	}

	t := &ToolSet{
		appID:          appID,
		installationID: installationID,
		privateKey:     privateKey,
		logger:         slog.Default(),
	}
	for _, opt := range opts {
		opt(t)
	}

	// Build the transport and client only when the test has not already
	// injected a fake client.
	if t.client == nil {
		transport, err := ghinstallation.New(
			http.DefaultTransport,
			t.appID,
			t.installationID,
			[]byte(t.privateKey),
		)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to create GitHub App transport")
		}
		t.transport = transport

		httpClient := &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		}
		t.client = &defaultGHClient{
			github:     ghlib.NewClient(httpClient),
			httpClient: httpClient,
		}
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

// buildTools constructs the seven typed GitHub tools. Each tool's schema is
// inferred from its typed input struct, eliminating the hand-written parameter
// map and the args["x"].(T) assertions in Run. MustNewTool is used because the
// In/Out types are static: a build failure is a programming error (already
// guarded by the package-level MustToolSchema), not a runtime condition New
// should report.
func (t *ToolSet) buildTools() []gollem.Tool {
	tools := make([]gollem.Tool, 0, 7)

	codeSearch := gollem.MustNewTool(
		"github_code_search",
		"Search for code across any GitHub repository reachable by the App installation. Query syntax examples: 'function login', 'language:go fmt.Println', 'path:src/ extension:js', 'filename:config NOT test'. Scope the search by passing repo_filter or by including 'repo:owner/name', 'org:owner', or 'user:owner' qualifiers in the query.",
		func(ctx context.Context, in codeSearchInput) (map[string]any, error) {
			if in.Query == "" {
				return nil, goerr.New("query is required")
			}
			return t.runCodeSearch(ctx, in)
		},
	)
	tools = append(tools, codeSearch)

	issueSearch := gollem.MustNewTool(
		"github_issue_search",
		"Search for issues and pull requests across any GitHub repository reachable by the App installation. Query syntax: 'bug in:title', 'label:security state:open', 'author:octocat type:pr'. Scope by passing repo_filter or by including 'repo:owner/name', 'org:owner', or 'user:owner' qualifiers in the query.",
		func(ctx context.Context, in issueSearchInput) (map[string]any, error) {
			if in.Query == "" {
				return nil, goerr.New("query is required")
			}
			return t.runIssueSearch(ctx, in)
		},
	)
	tools = append(tools, issueSearch)

	getContent := gollem.MustNewTool(
		"github_get_content",
		"Get file content from any GitHub repository reachable by the App installation. Returns the decoded content of the file.",
		func(ctx context.Context, in getContentInput) (map[string]any, error) {
			if in.Owner == "" {
				return nil, goerr.New("owner is required")
			}
			if in.Repo == "" {
				return nil, goerr.New("repo is required")
			}
			if in.Path == "" {
				return nil, goerr.New("path is required")
			}
			return t.runGetContent(ctx, in)
		},
	)
	tools = append(tools, getContent)

	listCommits := gollem.MustNewTool(
		"github_list_commits",
		"List commits for any repository reachable by the App installation. Supports filtering by file path, author, and branch/SHA. Useful for understanding change history and identifying who changed what and when.",
		func(ctx context.Context, in listCommitsInput) (map[string]any, error) {
			if in.Owner == "" {
				return nil, goerr.New("owner is required")
			}
			if in.Repo == "" {
				return nil, goerr.New("repo is required")
			}
			return t.runListCommits(ctx, in)
		},
	)
	tools = append(tools, listCommits)

	getBlame := gollem.MustNewTool(
		"github_get_blame",
		"Get git blame information for a file in any repository reachable by the App installation, showing which commit last modified each line. Useful for identifying who wrote specific code and when.",
		func(ctx context.Context, in getBlameInput) (map[string]any, error) {
			if in.Owner == "" {
				return nil, goerr.New("owner is required")
			}
			if in.Repo == "" {
				return nil, goerr.New("repo is required")
			}
			if in.Path == "" {
				return nil, goerr.New("path is required")
			}
			return t.runGetBlame(ctx, in)
		},
	)
	tools = append(tools, getBlame)

	getIssue := gollem.MustNewTool(
		"github_get_issue",
		"Fetch a single GitHub issue (not a pull request) by number, with full body, labels, and all comments. If the number resolves to a pull request, the call fails — use github_get_pull_request instead.",
		func(ctx context.Context, in getIssueInput) (map[string]any, error) {
			if in.Owner == "" {
				return nil, goerr.New("owner is required")
			}
			if in.Repo == "" {
				return nil, goerr.New("repo is required")
			}
			if in.Number < 1 {
				return nil, goerr.New("number is required and must be a positive integer")
			}
			return t.runGetIssue(ctx, in)
		},
	)
	tools = append(tools, getIssue)

	getPR := gollem.MustNewTool(
		"github_get_pull_request",
		"Fetch a single GitHub pull request by number, with body, labels, all comments, all reviews, and optionally the file diff. Use include_files=true only when the diff is needed; large PRs can return many files.",
		func(ctx context.Context, in getPullRequestInput) (map[string]any, error) {
			if in.Owner == "" {
				return nil, goerr.New("owner is required")
			}
			if in.Repo == "" {
				return nil, goerr.New("repo is required")
			}
			if in.Number < 1 {
				return nil, goerr.New("number is required and must be a positive integer")
			}
			return t.runGetPullRequest(ctx, in)
		},
	)
	tools = append(tools, getPR)

	return tools
}

// Ping verifies connectivity and credentials by fetching a short-lived
// installation token from the GitHub API.
func (t *ToolSet) Ping(ctx context.Context) error {
	if t.transport != nil {
		if _, err := t.transport.Token(ctx); err != nil {
			return goerr.Wrap(err, "GitHub ping failed: could not obtain installation token")
		}
		return nil
	}

	// When a fake client has been injected (e.g. in tests), perform a
	// minimal API call instead.
	_, _, err := t.client.ListCommits(ctx, "github", "gitignore", &ghlib.CommitsListOptions{
		ListOptions: ghlib.ListOptions{PerPage: 1},
	})
	if err != nil {
		return goerr.Wrap(err, "GitHub ping failed")
	}
	return nil
}

// Specs returns the tool specifications derived from the typed tool definitions.
func (t *ToolSet) Specs(_ context.Context) ([]gollem.ToolSpec, error) {
	specs := make([]gollem.ToolSpec, len(t.tools))
	for i, tool := range t.tools {
		specs[i] = tool.Spec()
	}
	return specs, nil
}

// Run dispatches to the matching typed tool by name.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	tool, ok := t.toolByName[name]
	if !ok {
		return nil, goerr.New("unknown tool name", goerr.V("name", name))
	}
	return tool.Run(ctx, args)
}
