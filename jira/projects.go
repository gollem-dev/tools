package jira

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gollem-dev/gollem"
	"github.com/m-mizutani/goerr/v2"
)

func listProjectsSpec() gollem.ToolSpec {
	return gollem.ToolSpec{
		Name: toolListProjects,
		Description: "List Jira projects accessible to the authenticated account. " +
			"Returns id, key, name, project type, and lead for each project, with pagination.",
		Parameters: map[string]*gollem.Parameter{
			"query": {
				Type:        gollem.TypeString,
				Description: "Filter projects whose name or key contains this substring. Omit to list all accessible projects.",
				Required:    false,
			},
			"max_results": {
				Type:        gollem.TypeInteger,
				Description: "Number of projects to return (1-50, default 50).",
				Required:    false,
			},
			"start_at": {
				Type:        gollem.TypeInteger,
				Description: "Zero-based index of the first project to return, for pagination. Default 0.",
				Required:    false,
			},
		},
	}
}

// projectSearchResponse mirrors the relevant fields of GET /rest/api/3/project/search.
type projectSearchResponse struct {
	IsLast bool          `json:"isLast"`
	Total  int           `json:"total"`
	Values []jiraProject `json:"values"`
}

type jiraProject struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	ProjectType string `json:"projectTypeKey"`
	Lead        struct {
		DisplayName string `json:"displayName"`
	} `json:"lead"`
}

func (t *ToolSet) listProjects(ctx context.Context, args map[string]any) (map[string]any, error) {
	maxResults := clampInt(args["max_results"], 50, 1, 50)
	// start_at defaults to 0; clampInt with def=0 yields 0 whether the arg is
	// absent or explicitly zero, which is exactly the desired first-page behaviour.
	startAt := clampInt(args["start_at"], 0, 0, 1<<30)

	q := url.Values{}
	q.Set("maxResults", strconv.Itoa(maxResults))
	q.Set("startAt", strconv.Itoa(startAt))
	// expand the lead so the result can report who owns each project.
	q.Set("expand", "lead")
	if v, ok := args["query"].(string); ok && v != "" {
		q.Set("query", v)
	}

	var resp projectSearchResponse
	if err := t.doJSON(ctx, http.MethodGet, "/rest/api/3/project/search?"+q.Encode(), nil, &resp); err != nil {
		return nil, goerr.Wrap(err, "failed to list jira projects")
	}

	items := make([]map[string]any, 0, len(resp.Values))
	for _, p := range resp.Values {
		items = append(items, map[string]any{
			"id":           p.ID,
			"key":          p.Key,
			"name":         p.Name,
			"project_type": p.ProjectType,
			"lead":         p.Lead.DisplayName,
		})
	}

	return map[string]any{
		"items":   items,
		"total":   resp.Total,
		"is_last": resp.IsLast,
	}, nil
}
