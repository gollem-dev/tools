package notion

import (
	"context"
	"net/http"
	"net/url"

	"github.com/m-mizutani/goerr/v2"
)

// getPageInput is the typed argument for notion_get_page.
type getPageInput struct {
	PageID string `json:"page_id" description:"The Notion page ID (with or without dashes)." required:"true"`
}

// markdownResponse is the JSON shape returned by GET /v1/pages/{id}/markdown.
type markdownResponse struct {
	Markdown  string `json:"markdown"`
	Truncated bool   `json:"truncated"`
	// UnknownBlockIDs lists the block IDs whose subtrees were omitted when the
	// page was truncated. Each can be passed back as page_id to fetch the missing
	// subtree, so callers can recover the full content of large pages.
	UnknownBlockIDs []string `json:"unknown_block_ids"`
}

func (t *ToolSet) runGetPage(ctx context.Context, in getPageInput) (map[string]any, error) {
	if in.PageID == "" {
		return nil, goerr.New("page_id is required")
	}

	// PathEscape: page_id arrives from LLM tool args, so guard against accidental
	// slashes / spaces / non-UUID characters that would break the URL or escape
	// the /v1/pages/ scope.
	path := "/v1/pages/" + url.PathEscape(in.PageID) + "/markdown"

	var resp markdownResponse
	if err := t.doJSON(ctx, http.MethodGet, path, markdownAPIVersion, nil, &resp); err != nil {
		return nil, goerr.Wrap(err, "failed to fetch notion page markdown", goerr.V("page_id", in.PageID))
	}

	unknownBlockIDs := resp.UnknownBlockIDs
	if unknownBlockIDs == nil {
		unknownBlockIDs = []string{}
	}

	return map[string]any{
		"page_id":           in.PageID,
		"markdown":          resp.Markdown,
		"truncated":         resp.Truncated,
		"unknown_block_ids": unknownBlockIDs,
	}, nil
}
