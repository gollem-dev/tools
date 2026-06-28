package jira

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/m-mizutani/goerr/v2"
)

// maxBulkIssues is the per-request cap Jira enforces on issue/bulkfetch.
const maxBulkIssues = 100

// getIssuesInput is the typed argument for the jira_get_issues tool.
// The schema is inferred from the struct tags, so there is no separate
// hand-written parameter map to drift from the Run implementation.
type getIssuesInput struct {
	IssueKeys       []string `json:"issue_keys" description:"Issue keys or ids to fetch (e.g. [\"PROJ-1\", \"PROJ-2\"]). Between 1 and 100 entries." required:"true"`
	IncludeComments bool     `json:"include_comments" description:"When true, include each issue's comments (author, created time, Markdown body). Default false."`
}

// bulkFetchResponse mirrors the relevant fields of POST /rest/api/3/issue/bulkfetch.
type bulkFetchResponse struct {
	Issues         []issueDetail `json:"issues"`
	IssueErrors    []string      `json:"issueErrors"`
	NotFoundIssues []string      `json:"notFoundIssueIds"`
	NotFoundKeys   []string      `json:"notFoundIssueKeys"`
}

// issueDetail holds the issue fields surfaced by jira_get_issues.
type issueDetail struct {
	Key    string `json:"key"`
	Fields struct {
		Summary     string          `json:"summary"`
		Description json.RawMessage `json:"description"`
		Created     string          `json:"created"`
		Updated     string          `json:"updated"`
		Labels      []string        `json:"labels"`
		Status      struct {
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
		Reporter struct {
			DisplayName string `json:"displayName"`
		} `json:"reporter"`
		Comment struct {
			Comments []issueComment `json:"comments"`
		} `json:"comment"`
	} `json:"fields"`
}

type issueComment struct {
	Created string          `json:"created"`
	Body    json.RawMessage `json:"body"`
	Author  struct {
		DisplayName string `json:"displayName"`
	} `json:"author"`
}

func (t *ToolSet) getIssues(ctx context.Context, in getIssuesInput) (map[string]any, error) {
	// Keep the explicit empty check: direct ToolSet.Run calls in tests bypass
	// gollem's required-field validation, so this check preserves the contract.
	if len(in.IssueKeys) == 0 {
		return nil, goerr.New("issue_keys must contain at least one key")
	}
	if len(in.IssueKeys) > maxBulkIssues {
		return nil, goerr.New("too many issue_keys", goerr.V("count", len(in.IssueKeys)), goerr.V("max", maxBulkIssues))
	}

	fields := []string{
		"summary", "description", "status", "issuetype",
		"priority", "assignee", "reporter", "labels", "created", "updated",
	}
	if in.IncludeComments {
		fields = append(fields, "comment")
	}

	body := map[string]any{
		"issueIdsOrKeys": in.IssueKeys,
		"fields":         fields,
	}

	var resp bulkFetchResponse
	if err := t.doJSON(ctx, http.MethodPost, "/rest/api/3/issue/bulkfetch", body, &resp); err != nil {
		return nil, goerr.Wrap(err, "failed to fetch jira issues")
	}

	items := make([]map[string]any, 0, len(resp.Issues))
	for _, is := range resp.Issues {
		item := map[string]any{
			"key":         is.Key,
			"summary":     is.Fields.Summary,
			"status":      is.Fields.Status.Name,
			"issue_type":  is.Fields.IssueType.Name,
			"priority":    is.Fields.Priority.Name,
			"assignee":    is.Fields.Assignee.DisplayName,
			"reporter":    is.Fields.Reporter.DisplayName,
			"labels":      orEmptySlice(is.Fields.Labels),
			"created":     is.Fields.Created,
			"updated":     is.Fields.Updated,
			"description": adfToMarkdown(is.Fields.Description),
		}
		if in.IncludeComments {
			comments := make([]map[string]any, 0, len(is.Fields.Comment.Comments))
			for _, c := range is.Fields.Comment.Comments {
				comments = append(comments, map[string]any{
					"author":  c.Author.DisplayName,
					"created": c.Created,
					"body":    adfToMarkdown(c.Body),
				})
			}
			item["comments"] = comments
		}
		items = append(items, item)
	}

	// Merge both not-found channels (by id and by key) into one list for the caller.
	notFound := make([]string, 0, len(resp.NotFoundKeys)+len(resp.NotFoundIssues))
	notFound = append(notFound, resp.NotFoundKeys...)
	notFound = append(notFound, resp.NotFoundIssues...)

	return map[string]any{
		"items":     items,
		"not_found": notFound,
	}, nil
}

// orEmptySlice returns s, or an empty (non-nil) slice when s is nil, so the
// result marshals to [] rather than null.
func orEmptySlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
