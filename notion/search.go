package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gollem-dev/gollem"
	"github.com/m-mizutani/goerr/v2"
)

func searchSpec() gollem.ToolSpec {
	return gollem.ToolSpec{
		Name: toolSearch,
		Description: "Search Notion pages and databases shared with the integration. " +
			"Matches titles against the query string. Returns id, type (page or database), title, URL, and last edited timestamp.",
		Parameters: map[string]*gollem.Parameter{
			"query": {
				Type:        gollem.TypeString,
				Description: "Title substring to search for. Pass an empty string to list all accessible pages/databases.",
				Required:    true,
			},
			"page_size": {
				Type:        gollem.TypeInteger,
				Description: "Number of results to return (1-100, default 20).",
				Required:    false,
			},
			"filter_type": {
				Type:        gollem.TypeString,
				Description: "Limit results to a specific object type. Omit for both pages and databases.",
				Required:    false,
				Enum:        []string{"page", "database"},
			},
			"sort": {
				Type:        gollem.TypeString,
				Description: "Order results by last edited time. Omit for Notion's default ordering.",
				Required:    false,
				Enum:        []string{"ascending", "descending"},
			},
			"start_cursor": {
				Type:        gollem.TypeString,
				Description: "Pagination cursor returned as next_cursor by a previous call. Omit to start from the beginning.",
				Required:    false,
			},
		},
	}
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

func (t *ToolSet) search(ctx context.Context, args map[string]any) (map[string]any, error) {
	// query may be empty (lists all accessible objects), but the caller must opt
	// in explicitly by passing the key.
	query, ok := args["query"].(string)
	if !ok {
		return nil, goerr.New("query is required (pass empty string to list all)")
	}

	body := map[string]any{
		"query":     query,
		"page_size": clampPageSize(args["page_size"]),
	}
	if v, ok := args["filter_type"].(string); ok && v != "" {
		body["filter"] = map[string]any{"property": "object", "value": v}
	}
	if v, ok := args["sort"].(string); ok && v != "" {
		body["sort"] = map[string]any{"timestamp": "last_edited_time", "direction": v}
	}
	if v, ok := args["start_cursor"].(string); ok && v != "" {
		body["start_cursor"] = v
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

// clampPageSize coerces a tool argument into a valid Notion page_size, defaulting
// to 20 and clamping to [1, 100].
func clampPageSize(v any) int {
	n := 0
	switch x := v.(type) {
	case float64: // JSON numbers decode to float64 through map[string]any
		n = int(x)
	case int:
		n = x
	case int64:
		n = int(x)
	}
	if n <= 0 {
		return 20
	}
	if n > 100 {
		return 100
	}
	return n
}
