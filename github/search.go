package github

import (
	"context"
	"fmt"
	"strings"

	ghlib "github.com/google/go-github/v74/github"
	"github.com/m-mizutani/goerr/v2"
)

func (t *ToolSet) runCodeSearch(ctx context.Context, args map[string]any) (map[string]any, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return nil, goerr.New("query is required")
	}

	searchQuery := buildCodeSearchQuery(query, args)

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
func buildCodeSearchQuery(baseQuery string, args map[string]any) string {
	parts := []string{baseQuery}

	if filters := parseRepoFilter(args); len(filters) > 0 {
		parts = append(parts, strings.Join(filters, " "))
	}
	if lang, ok := args["language"].(string); ok && lang != "" {
		parts = append(parts, fmt.Sprintf("language:%s", lang))
	}
	if path, ok := args["path"].(string); ok && path != "" {
		parts = append(parts, fmt.Sprintf("path:%s", path))
	}
	if filename, ok := args["filename"].(string); ok && filename != "" {
		parts = append(parts, fmt.Sprintf("filename:%s", filename))
	}
	return strings.Join(parts, " ")
}

func (t *ToolSet) runIssueSearch(ctx context.Context, args map[string]any) (map[string]any, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return nil, goerr.New("query is required")
	}

	searchQuery := buildIssueSearchQuery(query, args)

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
func buildIssueSearchQuery(baseQuery string, args map[string]any) string {
	parts := []string{baseQuery}

	if filters := parseRepoFilter(args); len(filters) > 0 {
		parts = append(parts, strings.Join(filters, " "))
	}
	if state, ok := args["state"].(string); ok && state != "" && state != "all" {
		parts = append(parts, fmt.Sprintf("state:%s", state))
	}
	if author, ok := args["author"].(string); ok && author != "" {
		parts = append(parts, fmt.Sprintf("author:%s", author))
	}
	if labels, ok := args["labels"].(string); ok && labels != "" {
		for label := range strings.SplitSeq(labels, ",") {
			label = strings.TrimSpace(label)
			if label != "" {
				parts = append(parts, fmt.Sprintf("label:%q", label))
			}
		}
	}
	if typeFilter, ok := args["type"].(string); ok && typeFilter != "" && typeFilter != "all" {
		switch typeFilter {
		case "issue":
			parts = append(parts, "type:issue")
		case "pr":
			parts = append(parts, "type:pr")
		}
	}
	return strings.Join(parts, " ")
}

// parseRepoFilter parses the "repo_filter" argument as a comma-separated list
// of "owner/name" entries and returns them as `repo:owner/name` qualifiers.
// Entries that do not contain "/" are skipped.
func parseRepoFilter(args map[string]any) []string {
	raw, ok := args["repo_filter"].(string)
	if !ok || raw == "" {
		return nil
	}

	var filters []string
	for entry := range strings.SplitSeq(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" || !strings.Contains(entry, "/") {
			continue
		}
		filters = append(filters, "repo:"+entry)
	}
	return filters
}
