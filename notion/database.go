package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/gollem-dev/gollem"
	"github.com/m-mizutani/goerr/v2"
)

func queryDatabaseSpec() gollem.ToolSpec {
	return gollem.ToolSpec{
		Name: toolQueryDatabase,
		Description: "Query the rows (pages) of a Notion database shared with the integration. " +
			"Returns each row's id, title, URL, last edited timestamp, and a flattened map of its " +
			"properties (title, text, number, select, multi_select, date, checkbox, url, email, phone, etc.).",
		Parameters: map[string]*gollem.Parameter{
			"database_id": {
				Type:        gollem.TypeString,
				Description: "The Notion database ID (with or without dashes).",
				Required:    true,
			},
			"page_size": {
				Type:        gollem.TypeInteger,
				Description: "Number of rows to return (1-100, default 20).",
				Required:    false,
			},
			"start_cursor": {
				Type:        gollem.TypeString,
				Description: "Pagination cursor returned as next_cursor by a previous call. Omit to start from the beginning.",
				Required:    false,
			},
		},
	}
}

// databaseQueryResponse mirrors the relevant fields of POST /v1/databases/{id}/query.
type databaseQueryResponse struct {
	Results    []notionObject `json:"results"`
	HasMore    bool           `json:"has_more"`
	NextCursor string         `json:"next_cursor"`
}

func (t *ToolSet) queryDatabase(ctx context.Context, args map[string]any) (map[string]any, error) {
	databaseID, _ := args["database_id"].(string)
	if databaseID == "" {
		return nil, goerr.New("database_id is required")
	}

	body := map[string]any{
		"page_size": clampPageSize(args["page_size"]),
	}
	if v, ok := args["start_cursor"].(string); ok && v != "" {
		body["start_cursor"] = v
	}

	// PathEscape: database_id arrives from LLM tool args; guard against characters
	// that would break the URL or escape the /v1/databases/ scope.
	path := "/v1/databases/" + url.PathEscape(databaseID) + "/query"

	var resp databaseQueryResponse
	if err := t.doJSON(ctx, http.MethodPost, path, apiVersion, body, &resp); err != nil {
		return nil, goerr.Wrap(err, "failed to query notion database", goerr.V("database_id", databaseID))
	}

	items := make([]map[string]any, 0, len(resp.Results))
	for _, row := range resp.Results {
		items = append(items, map[string]any{
			"id":          row.ID,
			"title":       row.title(),
			"url":         row.URL,
			"last_edited": row.LastEditedTime.Format(time.RFC3339),
			"properties":  flattenProperties(row.Properties),
		})
	}

	return map[string]any{
		"items":       items,
		"has_more":    resp.HasMore,
		"next_cursor": resp.NextCursor,
	}, nil
}

// richText is the shared shape of a Notion rich text fragment. Only plain_text
// is needed for read-oriented tools.
type richText struct {
	PlainText string `json:"plain_text"`
}

// propertyValue is a Notion page property. Only the fields used to flatten a
// property into a human-readable value are decoded; the rest are ignored.
type propertyValue struct {
	Type        string         `json:"type"`
	Title       []richText     `json:"title"`
	RichText    []richText     `json:"rich_text"`
	Number      *float64       `json:"number"`
	Select      *selectOption  `json:"select"`
	MultiSelect []selectOption `json:"multi_select"`
	Date        *dateValue     `json:"date"`
	Checkbox    *bool          `json:"checkbox"`
	URL         *string        `json:"url"`
	Email       *string        `json:"email"`
	PhoneNumber *string        `json:"phone_number"`
	People      []personValue  `json:"people"`
	Status      *selectOption  `json:"status"`
	CreatedTime *string        `json:"created_time"`
	Formula     *formulaValue  `json:"formula"`
}

type selectOption struct {
	Name string `json:"name"`
}

type dateValue struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type personValue struct {
	Name string `json:"name"`
}

type formulaValue struct {
	Type    string     `json:"type"`
	String  *string    `json:"string"`
	Number  *float64   `json:"number"`
	Boolean *bool      `json:"boolean"`
	Date    *dateValue `json:"date"`
}

// flattenProperties reduces a row's raw Notion properties to a map of
// human-readable values, keyed by property name. Each property is decoded
// individually and tolerantly: properties that fail to decode (e.g. a database
// schema shape rather than a page value) or that carry no simple scalar
// representation are omitted rather than guessed at.
func flattenProperties(props map[string]json.RawMessage) map[string]any {
	out := make(map[string]any, len(props))
	for name, raw := range props {
		var prop propertyValue
		if err := json.Unmarshal(raw, &prop); err != nil {
			continue
		}
		if v, ok := prop.value(); ok {
			out[name] = v
		}
	}
	return out
}

// value resolves a single property to a human-readable Go value. The bool
// reports whether the property had a representable value.
func (p propertyValue) value() (any, bool) {
	switch p.Type {
	case "title":
		return plainText(p.Title), true
	case "rich_text":
		return plainText(p.RichText), true
	case "number":
		if p.Number == nil {
			return nil, false
		}
		return *p.Number, true
	case "select":
		if p.Select == nil {
			return nil, false
		}
		return p.Select.Name, true
	case "status":
		if p.Status == nil {
			return nil, false
		}
		return p.Status.Name, true
	case "multi_select":
		names := make([]string, 0, len(p.MultiSelect))
		for _, o := range p.MultiSelect {
			names = append(names, o.Name)
		}
		return names, true
	case "date":
		if p.Date == nil {
			return nil, false
		}
		if p.Date.End != "" {
			return p.Date.Start + "/" + p.Date.End, true
		}
		return p.Date.Start, true
	case "checkbox":
		if p.Checkbox == nil {
			return nil, false
		}
		return *p.Checkbox, true
	case "url":
		return derefString(p.URL)
	case "email":
		return derefString(p.Email)
	case "phone_number":
		return derefString(p.PhoneNumber)
	case "created_time":
		return derefString(p.CreatedTime)
	case "people":
		names := make([]string, 0, len(p.People))
		for _, person := range p.People {
			names = append(names, person.Name)
		}
		return names, true
	case "formula":
		if p.Formula == nil {
			return nil, false
		}
		return p.Formula.value()
	default:
		return nil, false
	}
}

func (f formulaValue) value() (any, bool) {
	switch f.Type {
	case "string":
		return derefString(f.String)
	case "number":
		if f.Number == nil {
			return nil, false
		}
		return *f.Number, true
	case "boolean":
		if f.Boolean == nil {
			return nil, false
		}
		return *f.Boolean, true
	case "date":
		if f.Date == nil {
			return nil, false
		}
		if f.Date.End != "" {
			return f.Date.Start + "/" + f.Date.End, true
		}
		return f.Date.Start, true
	default:
		return nil, false
	}
}

func derefString(s *string) (any, bool) {
	if s == nil {
		return nil, false
	}
	return *s, true
}
