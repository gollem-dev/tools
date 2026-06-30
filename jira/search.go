package jira

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/m-mizutani/goerr/v2"
)

// searchIssuesInput is the typed argument for the jira_search_issues tool.
// The schema is inferred from the struct tags, so there is no separate
// hand-written parameter map to drift from the Run implementation.
type searchIssuesInput struct {
	JQL           string `json:"jql" description:"JQL query string, e.g. 'status = \"In Progress\" ORDER BY updated DESC'. Omit to match all issues (subject to the project filter below)."`
	Project       string `json:"project" description:"Restrict the search to this project key or id. Combined with jql via AND. Convenience for the common 'project = X' clause; you may instead put it directly in jql."`
	MaxResults    int    `json:"max_results" description:"Number of issues to return (1-100, default 50)."`
	NextPageToken string `json:"next_page_token" description:"Pagination token returned as next_page_token by a previous call. Omit to start from the first page."`
}

// issueSearchResponse mirrors the relevant fields of GET /rest/api/3/search/jql.
type issueSearchResponse struct {
	Issues        []searchIssue `json:"issues"`
	NextPageToken string        `json:"nextPageToken"`
	IsLast        bool          `json:"isLast"`
}

// searchIssue holds the subset of issue fields requested for search results.
type searchIssue struct {
	Key    string `json:"key"`
	Fields struct {
		Summary string `json:"summary"`
		Updated string `json:"updated"`
		Status  struct {
			Name string `json:"name"`
		} `json:"status"`
		IssueType struct {
			Name string `json:"name"`
		} `json:"issuetype"`
		Priority struct {
			Name string `json:"name"`
		} `json:"priority"`
		Assignee struct {
			DisplayName string `json:"displayName"`
		} `json:"assignee"`
	} `json:"fields"`
}

func (t *ToolSet) searchIssues(ctx context.Context, in searchIssuesInput) (map[string]any, error) {
	jql := in.JQL
	if in.Project != "" {
		jql = combineJQL(in.Project, jql)
	}

	maxResults := clampInt(in.MaxResults, 50, 1, 100)

	q := url.Values{}
	q.Set("jql", jql)
	q.Set("maxResults", strconv.Itoa(maxResults))
	// Request only the fields surfaced in the result to keep payloads small.
	q.Set("fields", "summary,status,issuetype,priority,assignee,updated")
	if in.NextPageToken != "" {
		q.Set("nextPageToken", in.NextPageToken)
	}

	var resp issueSearchResponse
	if err := t.doJSON(ctx, http.MethodGet, "/rest/api/3/search/jql?"+q.Encode(), nil, &resp); err != nil {
		return nil, goerr.Wrap(err, "failed to search jira issues", goerr.V("jql", jql))
	}

	items := make([]map[string]any, 0, len(resp.Issues))
	for _, is := range resp.Issues {
		items = append(items, map[string]any{
			"key":        is.Key,
			"summary":    is.Fields.Summary,
			"status":     is.Fields.Status.Name,
			"issue_type": is.Fields.IssueType.Name,
			"assignee":   is.Fields.Assignee.DisplayName,
			"priority":   is.Fields.Priority.Name,
			"updated":    is.Fields.Updated,
		})
	}

	return map[string]any{
		"items":           items,
		"next_page_token": resp.NextPageToken,
		"is_last":         resp.IsLast,
	}, nil
}

// combineJQL prepends a `project = "X"` clause to an existing JQL string via AND.
// When jql already carries an ORDER BY, the project clause must go before it, so
// the two halves are spliced around the ORDER BY keyword.
func combineJQL(project, jql string) string {
	clause := `project = ` + strconv.Quote(project)
	trimmed := strings.TrimSpace(jql)
	if trimmed == "" {
		return clause
	}

	// Split off a trailing ORDER BY (case-insensitive) so the AND lands on the
	// filter portion, not after the sort directive (which would be invalid JQL).
	upper := strings.ToUpper(trimmed)
	if idx := strings.LastIndex(upper, "ORDER BY"); idx >= 0 {
		filter := strings.TrimSpace(trimmed[:idx])
		order := trimmed[idx:]
		if filter == "" {
			return clause + " " + order
		}
		return clause + " AND (" + filter + ") " + order
	}
	return clause + " AND (" + trimmed + ")"
}
