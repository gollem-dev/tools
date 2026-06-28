package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"
)

// searchInput is the typed argument for notion_search. Query is a pointer to
// distinguish an absent key (nil) from an explicitly empty string (non-nil),
// because an empty query is valid (it lists all accessible pages/databases).
type searchInput struct {
	Query       *string `json:"query" description:"Title substring to search for. Pass an empty string to list all accessible pages/databases."`
	PageSize    int     `json:"page_size" description:"Number of results to return (1-100, default 20)." min:"1" max:"100"`
	FilterType  string  `json:"filter_type" description:"Limit results to a specific object type. Omit for both pages and databases." enum:"page,database"`
	Sort        string  `json:"sort" description:"Order results by last edited time. Omit for Notion's default ordering." enum:"ascending,descending"`
	StartCursor string  `json:"start_cursor" description:"Pagination cursor returned as next_cursor by a previous call. Omit to start from the beginning."`
}

// searchResponse mirrors the relevant fields of the POST /v1/search response.
type searchResponse struct {
	Results    []notionObject `json:"results"`
	HasMore    bool           `json:"has_more"`
	NextCursor string         `json:"next_cursor"`
}

// notionObject is the shared shape of a page or database in API responses.
// Title resolution differs by object type, so the raw properties/title fields
// are both retained and resolved in the title method.
type notionObject struct {
	Object         string    `json:"object"` // "page" or "database"
	ID             string    `json:"id"`
	URL            string    `json:"url"`
	LastEditedTime time.Time `json:"last_edited_time"`
	// Database title lives at the top level as an array of rich text.
	Title []richText `json:"title"`
	// Page property values and database property *schemas* share this key but have
	// incompatible shapes (e.g. a page's "rich_text" is an array, a database
	// schema's "rich_text" is an object), so each property is decoded lazily and
	// tolerantly rather than into a fixed type up front.
	Properties map[string]json.RawMessage `json:"properties"`
}

func (t *ToolSet) runSearch(ctx context.Context, in searchInput) (map[string]any, error) {
	// query may be empty (lists all accessible objects), but the caller must opt
	// in explicitly by providing the key; a missing key (nil pointer) is rejected.
	if in.Query == nil {
		return nil, goerr.New("query is required (pass empty string to list all)")
	}
	query := *in.Query

	pageSize := in.PageSize
	if pageSize <= 0 {
		pageSize = 20
	} else if pageSize > 100 {
		pageSize = 100
	}

	body := map[string]any{
		"query":     query,
		"page_size": pageSize,
	}
	if in.FilterType != "" {
		body["filter"] = map[string]any{"property": "object", "value": in.FilterType}
	}
	if in.Sort != "" {
		body["sort"] = map[string]any{"timestamp": "last_edited_time", "direction": in.Sort}
	}
	if in.StartCursor != "" {
		body["start_cursor"] = in.StartCursor
	}

	var resp searchResponse
	if err := t.doJSON(ctx, http.MethodPost, "/v1/search", apiVersion, body, &resp); err != nil {
		return nil, goerr.Wrap(err, "failed to search notion", goerr.V("query", query))
	}

	items := make([]map[string]any, 0, len(resp.Results))
	for _, obj := range resp.Results {
		items = append(items, map[string]any{
			"id":          obj.ID,
			"type":        obj.Object,
			"title":       obj.title(),
			"url":         obj.URL,
			"last_edited": obj.LastEditedTime.Format(time.RFC3339),
		})
	}

	return map[string]any{
		"items":       items,
		"has_more":    resp.HasMore,
		"next_cursor": resp.NextCursor,
	}, nil
}

// title resolves the human-readable title of a page or database object.
func (o notionObject) title() string {
	if len(o.Title) > 0 {
		return plainText(o.Title)
	}
	for _, raw := range o.Properties {
		var p propertyValue
		if err := json.Unmarshal(raw, &p); err != nil {
			continue
		}
		if p.Type == "title" {
			return plainText(p.Title)
		}
	}
	return ""
}

// plainText concatenates the plain_text of a rich text array.
func plainText(rts []richText) string {
	var sb strings.Builder
	for _, rt := range rts {
		sb.WriteString(rt.PlainText)
	}
	return sb.String()
}
