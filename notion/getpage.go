package notion

import (
	"context"
	"net/http"
	"net/url"

	"github.com/gollem-dev/gollem"
	"github.com/m-mizutani/goerr/v2"
)

func getPageSpec() gollem.ToolSpec {
	return gollem.ToolSpec{
		Name: toolGetPage,
		Description: "Retrieve a Notion page's full content as Notion-flavored Markdown. " +
			"The integration must have access to the page. Returns the markdown body and a 'truncated' flag " +
			"(true when the page exceeds Notion's render limits).",
		Parameters: map[string]*gollem.Parameter{
			"page_id": {
				Type:        gollem.TypeString,
				Description: "The Notion page ID (with or without dashes).",
				Required:    true,
			},
		},
	}
}

// markdownResponse is the JSON shape returned by GET /v1/pages/{id}/markdown.
type markdownResponse struct {
	Markdown  string `json:"markdown"`
	Truncated bool   `json:"truncated"`
}

func (t *ToolSet) getPage(ctx context.Context, args map[string]any) (map[string]any, error) {
	pageID, _ := args["page_id"].(string)
	if pageID == "" {
		return nil, goerr.New("page_id is required")
	}

	// PathEscape: page_id arrives from LLM tool args, so guard against accidental
	// slashes / spaces / non-UUID characters that would break the URL or escape
	// the /v1/pages/ scope.
	path := "/v1/pages/" + url.PathEscape(pageID) + "/markdown"

	var resp markdownResponse
	if err := t.doJSON(ctx, http.MethodGet, path, markdownAPIVersion, nil, &resp); err != nil {
		return nil, goerr.Wrap(err, "failed to fetch notion page markdown", goerr.V("page_id", pageID))
	}

	return map[string]any{
		"page_id":   pageID,
		"markdown":  resp.Markdown,
		"truncated": resp.Truncated,
	}, nil
}
