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
}

var _ gollem.ToolSet = (*ToolSet)(nil)

// Option configures a ToolSet.
type Option func(*ToolSet)

// WithAppID sets the GitHub App ID. It is required.
func WithAppID(id int64) Option {
	return func(t *ToolSet) { t.appID = id }
}

// WithInstallationID sets the GitHub App installation ID. It is required.
func WithInstallationID(id int64) Option {
	return func(t *ToolSet) { t.installationID = id }
}

// WithPrivateKey sets the GitHub App private key in PEM format. It is required.
func WithPrivateKey(pem string) Option {
	return func(t *ToolSet) { t.privateKey = pem }
}

// WithLogger sets the logger. A nil logger keeps the default (slog.Default()).
func WithLogger(logger *slog.Logger) Option {
	return func(t *ToolSet) {
		if logger != nil {
			t.logger = logger
		}
	}
}

// New constructs the ToolSet. It validates required fields and builds the
// GitHub client using the ghinstallation transport — both of which are purely
// local/in-memory operations. Use Ping to verify connectivity.
func New(opts ...Option) (*ToolSet, error) {
	t := &ToolSet{
		logger: slog.Default(),
	}
	for _, opt := range opts {
		opt(t)
	}

	if t.appID == 0 {
		return nil, goerr.New("GitHub App ID is required")
	}
	if t.installationID == 0 {
		return nil, goerr.New("GitHub App installation ID is required")
	}
	if t.privateKey == "" {
		return nil, goerr.New("GitHub App private key is required")
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

	return t, nil
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

// Specs returns the tool specifications for the five GitHub tools.
func (t *ToolSet) Specs(_ context.Context) ([]gollem.ToolSpec, error) {
	intPtr := func(v int) *int { return &v }

	return []gollem.ToolSpec{
		{
			Name:        "github_code_search",
			Description: "Search for code across any GitHub repository reachable by the App installation. Query syntax examples: 'function login', 'language:go fmt.Println', 'path:src/ extension:js', 'filename:config NOT test'. Scope the search by passing repo_filter or by including 'repo:owner/name', 'org:owner', or 'user:owner' qualifiers in the query.",
			Parameters: map[string]*gollem.Parameter{
				"query": {
					Type:        gollem.TypeString,
					Description: "Search query using GitHub code search syntax. Supports operators like AND, OR, NOT",
					Required:    true,
					MinLength:   intPtr(1),
				},
				"language": {
					Type:        gollem.TypeString,
					Description: "Filter by programming language (e.g., 'go', 'python', 'javascript')",
					Pattern:     "^[a-zA-Z0-9+#-]+$",
				},
				"path": {
					Type:        gollem.TypeString,
					Description: "Filter by file path pattern (e.g., 'src/', 'test/', '*.go')",
				},
				"filename": {
					Type:        gollem.TypeString,
					Description: "Filter by filename (e.g., 'config.yaml', 'main.go')",
					Pattern:     "^[^/]+$",
				},
				"repo_filter": {
					Type:        gollem.TypeString,
					Description: "Optional repository scope as a comma-separated list of 'owner/name' entries (e.g. 'octocat/Hello-World,octocat/Spoon-Knife'). When omitted, the search is not scoped to any specific repos; use 'repo:', 'org:', or 'user:' qualifiers in the query for finer control.",
				},
			},
		},
		{
			Name:        "github_issue_search",
			Description: "Search for issues and pull requests across any GitHub repository reachable by the App installation. Query syntax: 'bug in:title', 'label:security state:open', 'author:octocat type:pr'. Scope by passing repo_filter or by including 'repo:owner/name', 'org:owner', or 'user:owner' qualifiers in the query.",
			Parameters: map[string]*gollem.Parameter{
				"query": {
					Type:        gollem.TypeString,
					Description: "Search query using GitHub issue search syntax. Supports operators like in:title, in:body",
					Required:    true,
					MinLength:   intPtr(1),
				},
				"state": {
					Type:        gollem.TypeString,
					Description: "Filter by state: 'open', 'closed', or 'all'",
					Enum:        []string{"open", "closed", "all"},
					Default:     "all",
				},
				"labels": {
					Type:        gollem.TypeString,
					Description: "Filter by labels (comma-separated list, e.g., 'bug,help wanted')",
					Pattern:     "^[a-zA-Z0-9-_,\\s]+$",
				},
				"author": {
					Type:        gollem.TypeString,
					Description: "Filter by author username (GitHub username)",
					Pattern:     "^[a-zA-Z0-9][a-zA-Z0-9-]*$",
					MaxLength:   intPtr(39),
				},
				"type": {
					Type:        gollem.TypeString,
					Description: "Filter by type: 'issue' for issues only, 'pr' for pull requests only, or 'all' for both",
					Enum:        []string{"issue", "pr", "all"},
					Default:     "all",
				},
				"repo_filter": {
					Type:        gollem.TypeString,
					Description: "Optional repository scope as a comma-separated list of 'owner/name' entries. When omitted, the search is not scoped to any specific repos; use 'repo:', 'org:', or 'user:' qualifiers in the query for finer control.",
				},
			},
		},
		{
			Name:        "github_get_content",
			Description: "Get file content from any GitHub repository reachable by the App installation. Returns the decoded content of the file.",
			Parameters: map[string]*gollem.Parameter{
				"owner": {
					Type:        gollem.TypeString,
					Description: "Repository owner (organization or username)",
					Required:    true,
					Pattern:     "^[a-zA-Z0-9][a-zA-Z0-9-]*$",
					MinLength:   intPtr(1),
					MaxLength:   intPtr(39),
				},
				"repo": {
					Type:        gollem.TypeString,
					Description: "Repository name",
					Required:    true,
					Pattern:     "^[a-zA-Z0-9_.-]+$",
					MinLength:   intPtr(1),
					MaxLength:   intPtr(100),
				},
				"path": {
					Type:        gollem.TypeString,
					Description: "File path in the repository (e.g., 'src/main.go', 'README.md')",
					Required:    true,
					MinLength:   intPtr(1),
				},
				"ref": {
					Type:        gollem.TypeString,
					Description: "Git reference: branch name (e.g., 'main'), tag (e.g., 'v1.0.0'), or commit SHA. Defaults to the default branch if not specified.",
					Pattern:     "^[a-zA-Z0-9/_.-]+$",
				},
			},
		},
		{
			Name:        "github_list_commits",
			Description: "List commits for any repository reachable by the App installation. Supports filtering by file path, author, and branch/SHA. Useful for understanding change history and identifying who changed what and when.",
			Parameters: map[string]*gollem.Parameter{
				"owner": {
					Type:        gollem.TypeString,
					Description: "Repository owner (organization or username)",
					Required:    true,
					Pattern:     "^[a-zA-Z0-9][a-zA-Z0-9-]*$",
					MinLength:   intPtr(1),
					MaxLength:   intPtr(39),
				},
				"repo": {
					Type:        gollem.TypeString,
					Description: "Repository name",
					Required:    true,
					Pattern:     "^[a-zA-Z0-9_.-]+$",
					MinLength:   intPtr(1),
					MaxLength:   intPtr(100),
				},
				"sha": {
					Type:        gollem.TypeString,
					Description: "SHA or branch to start listing commits from. Defaults to the default branch.",
				},
				"path": {
					Type:        gollem.TypeString,
					Description: "Only commits containing this file path will be returned (e.g., 'src/main.go')",
				},
				"author": {
					Type:        gollem.TypeString,
					Description: "GitHub login or email address to filter commits by author",
				},
				"per_page": {
					Type:        gollem.TypeInteger,
					Description: "Number of commits per page (default: 30, max: 100)",
				},
				"page": {
					Type:        gollem.TypeInteger,
					Description: "Page number for pagination (default: 1)",
				},
			},
		},
		{
			Name:        "github_get_blame",
			Description: "Get git blame information for a file in any repository reachable by the App installation, showing which commit last modified each line. Useful for identifying who wrote specific code and when.",
			Parameters: map[string]*gollem.Parameter{
				"owner": {
					Type:        gollem.TypeString,
					Description: "Repository owner (organization or username)",
					Required:    true,
					Pattern:     "^[a-zA-Z0-9][a-zA-Z0-9-]*$",
					MinLength:   intPtr(1),
					MaxLength:   intPtr(39),
				},
				"repo": {
					Type:        gollem.TypeString,
					Description: "Repository name",
					Required:    true,
					Pattern:     "^[a-zA-Z0-9_.-]+$",
					MinLength:   intPtr(1),
					MaxLength:   intPtr(100),
				},
				"path": {
					Type:        gollem.TypeString,
					Description: "File path in the repository (e.g., 'src/main.go')",
					Required:    true,
					MinLength:   intPtr(1),
				},
				"ref": {
					Type:        gollem.TypeString,
					Description: "Git reference: branch name, tag, or commit SHA. Defaults to the repository's default branch.",
					Pattern:     "^[a-zA-Z0-9/_.-]+$",
				},
			},
		},
	}, nil
}

// Run executes the named GitHub tool.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	switch name {
	case "github_code_search":
		return t.runCodeSearch(ctx, args)
	case "github_issue_search":
		return t.runIssueSearch(ctx, args)
	case "github_get_content":
		return t.runGetContent(ctx, args)
	case "github_list_commits":
		return t.runListCommits(ctx, args)
	case "github_get_blame":
		return t.runGetBlame(ctx, args)
	default:
		return nil, goerr.New("unknown tool name", goerr.V("name", name))
	}
}
