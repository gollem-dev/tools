package falcon

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/m-mizutani/goerr/v2"
)

// Per-request upstream page sizes. The internal pagination loop walks these in
// chunks up to maxFetchRecords; they match each endpoint's documented maximum.
const (
	incidentsPerRequest = 500
	behaviorsPerRequest = 500
	devicesPerRequest   = 5000
	alertsPerRequest    = 1000
)

// pageTokenArg returns the page_token argument if present and non-empty.
func pageTokenArg(args map[string]any) (string, bool) {
	tok, ok := args["page_token"].(string)
	return tok, ok && tok != ""
}

// searchIncidents searches incident IDs via FQL and returns full details paged.
func (t *ToolSet) searchIncidents(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tok, ok := pageTokenArg(args); ok {
		return t.takePage(tok, t.maxRecords)
	}
	params := buildQueryParams(args, "filter", "sort")
	all, total, truncated, err := t.fetchOffset(ctx, "/incidents/queries/incidents/v1", params, incidentsPerRequest)
	if err != nil {
		return nil, goerr.Wrap(err, "incident search failed")
	}
	return t.buildSearchResult(all, clampLimit(args, t.maxRecords), total, truncated)
}

// searchBehaviors searches behavior IDs via FQL and returns them paged.
func (t *ToolSet) searchBehaviors(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tok, ok := pageTokenArg(args); ok {
		return t.takePage(tok, t.maxRecords)
	}
	params := buildQueryParams(args, "filter")
	all, total, truncated, err := t.fetchOffset(ctx, "/incidents/queries/behaviors/v1", params, behaviorsPerRequest)
	if err != nil {
		return nil, goerr.Wrap(err, "behavior search failed")
	}
	return t.buildSearchResult(all, clampLimit(args, t.maxRecords), total, truncated)
}

// searchDevices searches device IDs via FQL (devices-scroll) and returns them paged.
func (t *ToolSet) searchDevices(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tok, ok := pageTokenArg(args); ok {
		return t.takePage(tok, t.maxRecords)
	}
	params := buildQueryParams(args, "filter", "sort")
	all, total, truncated, err := t.fetchScroll(ctx, "/devices/queries/devices-scroll/v1", params, devicesPerRequest)
	if err != nil {
		return nil, goerr.Wrap(err, "device search failed")
	}
	return t.buildSearchResult(all, clampLimit(args, t.maxRecords), total, truncated)
}

// searchAlerts searches and retrieves full alert objects via FQL, paged.
func (t *ToolSet) searchAlerts(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tok, ok := pageTokenArg(args); ok {
		return t.takePage(tok, t.maxRecords)
	}
	filter, _ := args["filter"].(string)
	sort, _ := args["sort"].(string)
	all, total, truncated, err := t.fetchAlerts(ctx, filter, sort)
	if err != nil {
		return nil, goerr.Wrap(err, "alert search failed")
	}
	return t.buildSearchResult(all, clampLimit(args, t.maxRecords), total, truncated)
}

// getIncidents retrieves incident details by IDs.
func (t *ToolSet) getIncidents(ctx context.Context, args map[string]any) (map[string]any, error) {
	return t.getEntities(ctx, args, "ids", "ids", http.MethodPost, "/incidents/entities/incidents/GET/v1")
}

// getBehaviors retrieves behavior details by IDs.
func (t *ToolSet) getBehaviors(ctx context.Context, args map[string]any) (map[string]any, error) {
	return t.getEntities(ctx, args, "ids", "ids", http.MethodPost, "/incidents/entities/behaviors/GET/v1")
}

// getDevices retrieves device details by IDs.
func (t *ToolSet) getDevices(ctx context.Context, args map[string]any) (map[string]any, error) {
	return t.getEntities(ctx, args, "ids", "ids", http.MethodPost, "/devices/entities/devices/v2")
}

// getAlerts retrieves alert details by composite IDs.
func (t *ToolSet) getAlerts(ctx context.Context, args map[string]any) (map[string]any, error) {
	return t.getEntities(ctx, args, "composite_ids", "composite_ids", http.MethodPost, "/alerts/entities/alerts/v2")
}

// getEntities is the shared body for the get_* tools: it reads a comma-separated
// ID argument, clamps it to maxRecords to bound the response, POSTs the IDs, and
// notes any dropped IDs so the LLM can fetch the rest in a follow-up call.
func (t *ToolSet) getEntities(ctx context.Context, args map[string]any, argKey, bodyField, method, path string) (map[string]any, error) {
	raw, ok := args[argKey].(string)
	if !ok || raw == "" {
		return nil, goerr.New(argKey + " is required")
	}
	ids := splitAndTrim(raw)
	if len(ids) == 0 {
		return nil, goerr.New(argKey + " is required")
	}

	ids, dropped := clampIDs(ids, t.maxRecords)
	resp, err := t.doRequest(ctx, method, path, map[string]any{bodyField: ids})
	if err != nil {
		return nil, err
	}
	if dropped > 0 {
		resp["note"] = noteDroppedIDs(len(ids), dropped, t.maxRecords)
	}
	return resp, nil
}

// getCrowdScores retrieves CrowdScore values, truncated to maxRecords.
func (t *ToolSet) getCrowdScores(ctx context.Context, args map[string]any) (map[string]any, error) {
	params := buildQueryParams(args, "filter")
	path := "/incidents/combined/crowdscores/v1"
	if encoded := params.Encode(); encoded != "" {
		path += "?" + encoded
	}
	resp, err := t.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, goerr.Wrap(err, "CrowdScore retrieval failed")
	}

	res := extractResources(resp)
	truncated := false
	if len(res) > t.maxRecords {
		res = res[:t.maxRecords]
		truncated = true
	}
	result := map[string]any{
		"records":   res,
		"count":     len(res),
		"truncated": truncated,
	}
	if truncated {
		result["note"] = noteCrowdScoresTruncated(t.maxRecords)
	}
	return result, nil
}

// fetchOffset walks an offset/limit paginated query (incidents, behaviors) up to
// maxFetchRecords, returning the collected resources, the upstream total, and
// whether more remained upstream than was fetched.
func (t *ToolSet) fetchOffset(ctx context.Context, path string, params url.Values, perRequest int) ([]any, int, bool, error) {
	var all []any
	total := 0
	offset := 0

	for len(all) < t.maxFetchRecords {
		limit := perRequest
		if want := t.maxFetchRecords - len(all); want < limit {
			limit = want
		}
		p := cloneValues(params)
		p.Set("limit", strconv.Itoa(limit))
		p.Set("offset", strconv.Itoa(offset))

		resp, err := t.doRequest(ctx, http.MethodGet, path+"?"+p.Encode(), nil)
		if err != nil {
			return nil, 0, false, err
		}
		res := extractResources(resp)
		total = extractTotal(resp, total)
		all = append(all, res...)
		offset += len(res)

		if len(res) == 0 || (total > 0 && offset >= total) {
			break
		}
	}

	return all, total, total > len(all), nil
}

// fetchScroll walks the devices-scroll string-offset pagination up to
// maxFetchRecords.
func (t *ToolSet) fetchScroll(ctx context.Context, path string, params url.Values, perRequest int) ([]any, int, bool, error) {
	var all []any
	total := 0
	offsetTok := ""

	for len(all) < t.maxFetchRecords {
		limit := perRequest
		if want := t.maxFetchRecords - len(all); want < limit {
			limit = want
		}
		p := cloneValues(params)
		p.Set("limit", strconv.Itoa(limit))
		if offsetTok != "" {
			p.Set("offset", offsetTok)
		}

		resp, err := t.doRequest(ctx, http.MethodGet, path+"?"+p.Encode(), nil)
		if err != nil {
			return nil, 0, false, err
		}
		res := extractResources(resp)
		total = extractTotal(resp, total)
		all = append(all, res...)
		offsetTok = extractStringOffset(resp)

		if len(res) == 0 || offsetTok == "" || (total > 0 && len(all) >= total) {
			break
		}
	}

	return all, total, total > len(all), nil
}

// fetchAlerts walks the combined alerts "after"-cursor pagination up to
// maxFetchRecords, fetching full alert objects.
func (t *ToolSet) fetchAlerts(ctx context.Context, filter, sort string) ([]any, int, bool, error) {
	var all []any
	total := 0
	after := ""

	for len(all) < t.maxFetchRecords {
		limit := alertsPerRequest
		if want := t.maxFetchRecords - len(all); want < limit {
			limit = want
		}
		body := map[string]any{"limit": limit}
		if filter != "" {
			body["filter"] = filter
		}
		if sort != "" {
			body["sort"] = sort
		}
		if after != "" {
			body["after"] = after
		}

		resp, err := t.doRequest(ctx, http.MethodPost, "/alerts/combined/alerts/v1", body)
		if err != nil {
			return nil, 0, false, err
		}
		res := extractResources(resp)
		total = extractTotal(resp, total)
		all = append(all, res...)
		after = extractAfter(resp)

		if len(res) == 0 || after == "" || (total > 0 && len(all) >= total) {
			break
		}
	}

	return all, total, total > len(all), nil
}
