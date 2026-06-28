package github

import (
	"context"
	"fmt"
	"strings"

	ghlib "github.com/google/go-github/v74/github"
	"github.com/m-mizutani/goerr/v2"
)

// codeSearchInput is the typed argument struct for github_code_search.
type codeSearchInput struct {
	Query      string `json:"query" description:"Search query using GitHub code search syntax. Supports operators like AND, OR, NOT" required:"true" minLength:"1"`
	Language   string `json:"language" description:"Filter by programming language (e.g., 'go', 'python', 'javascript')" pattern:"^[a-zA-Z0-9+#-]+$"`
	Path       string `json:"path" description:"Filter by file path pattern (e.g., 'src/', 'test/', '*.go')"`
	Filename   string `json:"filename" description:"Filter by filename (e.g., 'config.yaml', 'main.go')" pattern:"^[^/]+$"`
	RepoFilter string `json:"repo_filter" description:"Optional repository scope as a comma-separated list of 'owner/name' entries (e.g. 'octocat/Hello-World,octocat/Spoon-Knife'). When omitted, the search is not scoped to any specific repos; use 'repo:', 'org:', or 'user:' qualifiers in the query for finer control."`
}

// issueSearchInput is the typed argument struct for github_issue_search.
type issueSearchInput struct {
	Query      string `json:"query" description:"Search query using GitHub issue search syntax. Supports operators like in:title, in:body" required:"true" minLength:"1"`
	State      string `json:"state" description:"Filter by state: 'open', 'closed', or 'all'" enum:"open,closed,all"`
	Labels     string `json:"labels" description:"Filter by labels (comma-separated list, e.g., 'bug,help wanted')" pattern:"^[a-zA-Z0-9-_,\\s]+$"`
	Author     string `json:"author" description:"Filter by author username (GitHub username)" pattern:"^[a-zA-Z0-9][a-zA-Z0-9-]*$" maxLength:"39"`
	Type       string `json:"type" description:"Filter by type: 'issue' for issues only, 'pr' for pull requests only, or 'all' for both" enum:"issue,pr,all"`
	RepoFilter string `json:"repo_filter" description:"Optional repository scope as a comma-separated list of 'owner/name' entries. When omitted, the search is not scoped to any specific repos; use 'repo:', 'org:', or 'user:' qualifiers in the query for finer control."`
}

func (t *ToolSet) runCodeSearch(ctx context.Context, in codeSearchInput) (map[string]any, error) {
	searchQuery := buildCodeSearchQuery(in)

	opts := &ghlib.SearchOptions{
		ListOptions: ghlib.ListOptions{PerPage: 30},
	}

	result, _, err := t.client.SearchCode(ctx, searchQuery, opts)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to search code", goerr.V("query", searchQuery))
	}

	var results []CodeSearchResult
	for _, item := range result.CodeResults {
		if item.Repository == nil || item.Repository.FullName == nil || item.Path == nil {
			continue
		}

		csr := CodeSearchResult{
			Repository: *item.Repository.FullName,
			Path:       *item.Path,
		}
		if item.HTMLURL != nil {
			csr.HTMLURL = *item.HTMLURL
		}
		for _, tm := range item.TextMatches {
			if tm.Fragment != nil {
				csr.Matches = append(csr.Matches, *tm.Fragment)
			}
		}
		results = append(results, csr)
	}

	total := 0
	if result.Total != nil {
		total = *result.Total
	}

	return map[string]any{
		"results": results,
		"total":   total,
	}, nil
}

// buildCodeSearchQuery appends optional qualifiers to the base query string.
func buildCodeSearchQuery(in codeSearchInput) string {
	parts := []string{in.Query}

	if filters := parseRepoFilter(in.RepoFilter); len(filters) > 0 {
		parts = append(parts, strings.Join(filters, " "))
	}
	if in.Language != "" {
		parts = append(parts, fmt.Sprintf("language:%s", in.Language))
	}
	if in.Path != "" {
		parts = append(parts, fmt.Sprintf("path:%s", in.Path))
	}
	if in.Filename != "" {
		parts = append(parts, fmt.Sprintf("filename:%s", in.Filename))
	}
	return strings.Join(parts, " ")
}

func (t *ToolSet) runIssueSearch(ctx context.Context, in issueSearchInput) (map[string]any, error) {
	searchQuery := buildIssueSearchQuery(in)

	opts := &ghlib.SearchOptions{
		ListOptions: ghlib.ListOptions{PerPage: 30},
	}

	result, _, err := t.client.SearchIssues(ctx, searchQuery, opts)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to search issues", goerr.V("query", searchQuery))
	}

	results := make([]IssueSearchResult, 0, len(result.Issues))
	for _, issue := range result.Issues {
		if issue.Number == nil || issue.Title == nil {
			continue
		}

		isr := IssueSearchResult{
			Number: *issue.Number,
			Title:  *issue.Title,
		}
		if issue.CreatedAt != nil {
			isr.CreatedAt = issue.CreatedAt.Time
		}
		if issue.UpdatedAt != nil {
			isr.UpdatedAt = issue.UpdatedAt.Time
		}
		if issue.RepositoryURL != nil {
			parts := strings.Split(*issue.RepositoryURL, "/")
			if len(parts) >= 2 {
				isr.Repository = fmt.Sprintf("%s/%s", parts[len(parts)-2], parts[len(parts)-1])
			}
		}
		if issue.State != nil {
			isr.State = *issue.State
		}
		if issue.HTMLURL != nil {
			isr.HTMLURL = *issue.HTMLURL
		}
		if issue.User != nil && issue.User.Login != nil {
			isr.User = *issue.User.Login
		}
		if issue.Body != nil {
			body := *issue.Body
			if len(body) > 500 {
				body = body[:500] + "..."
			}
			isr.Body = body
		}
		isr.IsPR = issue.IsPullRequest()
		for _, label := range issue.Labels {
			if label.Name != nil {
				isr.Labels = append(isr.Labels, *label.Name)
			}
		}

		results = append(results, isr)
	}

	total := 0
	if result.Total != nil {
		total = *result.Total
	}

	return map[string]any{
		"results": results,
		"total":   total,
	}, nil
}

// buildIssueSearchQuery appends optional qualifiers to the base query string.
func buildIssueSearchQuery(in issueSearchInput) string {
	parts := []string{in.Query}

	if filters := parseRepoFilter(in.RepoFilter); len(filters) > 0 {
		parts = append(parts, strings.Join(filters, " "))
	}
	if in.State != "" && in.State != "all" {
		parts = append(parts, fmt.Sprintf("state:%s", in.State))
	}
	if in.Author != "" {
		parts = append(parts, fmt.Sprintf("author:%s", in.Author))
	}
	if in.Labels != "" {
		for label := range strings.SplitSeq(in.Labels, ",") {
			label = strings.TrimSpace(label)
			if label != "" {
				parts = append(parts, fmt.Sprintf("label:%q", label))
			}
		}
	}
	if in.Type != "" && in.Type != "all" {
		switch in.Type {
		case "issue":
			parts = append(parts, "type:issue")
		case "pr":
			parts = append(parts, "type:pr")
		}
	}
	return strings.Join(parts, " ")
}

// parseRepoFilter parses a comma-separated list of "owner/name" entries and
// returns them as `repo:owner/name` qualifiers. Entries without "/" are skipped.
func parseRepoFilter(repoFilter string) []string {
	if repoFilter == "" {
		return nil
	}

	var filters []string
	for entry := range strings.SplitSeq(repoFilter, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" || !strings.Contains(entry, "/") {
			continue
		}
		filters = append(filters, "repo:"+entry)
	}
	return filters
}
