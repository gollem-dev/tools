package falcon

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/m-mizutani/goerr/v2"
)

// clampLimit reads the optional "limit" argument (a page size) and clamps it to
// [1, max]. When limit is absent or non-positive, max is used. limit controls
// only how many records are returned per page, never how many are fetched from
// Falcon (that is governed by maxFetchRecords).
func clampLimit(args map[string]any, max int) int {
	n := max
	if v, ok := args["limit"].(float64); ok && v >= 1 {
		n = int(v)
	}
	if n > max {
		n = max
	}
	if n < 1 {
		n = 1
	}
	return n
}

// clampIDs limits a slice of IDs to max entries, returning the kept IDs and the
// number dropped. Used by the get_* tools to bound the size of the detail
// payload returned to the LLM.
func clampIDs(ids []string, max int) (kept []string, dropped int) {
	if len(ids) <= max {
		return ids, 0
	}
	return ids[:max], len(ids) - max
}

// paginate splits all into a first page of at most pageSize records and stores
// the remainder (if any) in the page store, returning a token to fetch it.
func (t *ToolSet) paginate(all []any, pageSize int) (page []any, token string, hasMore bool, err error) {
	n := min(pageSize, len(all))
	page = all[:n]
	rest := all[n:]
	if len(rest) == 0 {
		return page, "", false, nil
	}
	token, err = t.pages.put(rest)
	if err != nil {
		return nil, "", false, err
	}
	return page, token, true, nil
}

// buildSearchResult assembles the uniform pagination envelope for a search that
// fetched `all` records into memory (capped at maxFetchRecords). totalAvailable
// is the upstream grand total when known (0 if unavailable); truncated reports
// whether the upstream had more than was fetched.
func (t *ToolSet) buildSearchResult(all []any, pageSize, totalAvailable int, truncated bool) (map[string]any, error) {
	page, token, hasMore, err := t.paginate(all, pageSize)
	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"records":   page,
		"count":     len(page),
		"total":     len(all),
		"has_more":  hasMore,
		"truncated": truncated,
	}
	if hasMore {
		result["page_token"] = token
	}
	if truncated && totalAvailable > len(all) {
		result["total_available"] = totalAvailable
	}
	result["note"] = searchNote(len(page), len(all), hasMore, truncated, totalAvailable)
	return result, nil
}

// takePage serves the next page for a previously issued page_token. The same
// token stays valid until its records are exhausted.
func (t *ToolSet) takePage(token string, pageSize int) (map[string]any, error) {
	page, remaining, ok := t.pages.take(token, pageSize)
	if !ok {
		return nil, goerr.New("unknown or expired page_token; re-run the search to get fresh results",
			goerr.V("page_token", token))
	}

	result := map[string]any{
		"records":  page,
		"count":    len(page),
		"has_more": remaining > 0,
	}
	if remaining > 0 {
		result["page_token"] = token
		result["note"] = fmt.Sprintf("Returned %d records; %d remain in memory. Call this tool again with the same page_token for the next page.", len(page), remaining)
	} else {
		result["note"] = fmt.Sprintf("Returned the final %d records. No more pages.", len(page))
	}
	return result, nil
}

// searchNote builds a human-readable hint describing the page and whether more
// data exists, in memory (has_more) or upstream (truncated).
func searchNote(returned, held int, hasMore, truncated bool, totalAvailable int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Returned %d of %d records held in memory.", returned, held)
	if hasMore {
		b.WriteString(" Call this tool again with page_token to fetch the next page.")
	}
	if truncated {
		if totalAvailable > held {
			fmt.Fprintf(&b, " %d records matched in total; only the first %d are paginable here — narrow the filter to reach the rest.", totalAvailable, held)
		} else {
			fmt.Fprintf(&b, " More than %d records matched; only the first %d were retrieved — narrow the query (e.g. a tighter time range or | head()) to reach the rest.", held, held)
		}
	}
	return b.String()
}

// noteDroppedIDs explains that a get_* call clamped its input ID list.
func noteDroppedIDs(fetched, dropped, max int) string {
	return fmt.Sprintf("Only the first %d IDs were fetched; %d were dropped (max %d per call). Call this tool again with the remaining IDs.", fetched, dropped, max)
}

// noteCrowdScoresTruncated explains that the CrowdScore list was truncated.
func noteCrowdScoresTruncated(max int) string {
	return fmt.Sprintf("More than %d CrowdScores matched; only the first %d are shown. Narrow the filter (e.g. timestamp) to see the rest.", max, max)
}

// buildQueryParams constructs URL query parameters from selected tool arguments.
func buildQueryParams(args map[string]any, keys ...string) url.Values {
	params := url.Values{}
	for _, key := range keys {
		val, ok := args[key]
		if !ok {
			continue
		}
		switch v := val.(type) {
		case string:
			if v != "" {
				params.Set(key, v)
			}
		case float64:
			params.Set(key, fmt.Sprintf("%d", int(v)))
		}
	}
	return params
}

// splitAndTrim splits a comma-separated string and trims whitespace, dropping
// empty elements.
func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// cloneValues returns a shallow copy of v so per-request mutations (limit,
// offset) do not leak across the internal pagination loop.
func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vs := range v {
		cp := make([]string, len(vs))
		copy(cp, vs)
		out[k] = cp
	}
	return out
}
