package falcon

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/m-mizutani/goerr/v2"
)

const (
	eventsMaxPolls     = 60
	eventsPollInterval = 2 * time.Second
)

// searchEvents runs a CQL query via the Next-Gen SIEM Search API. It creates a
// query job, polls until the job is done or maxFetchRecords events have been
// collected, keeps the overflow in memory, and returns the first page. Pass
// page_token to fetch subsequent pages without re-running the query.
func (t *ToolSet) searchEvents(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tok, ok := pageTokenArg(args); ok {
		return t.takePage(tok, t.maxRecords)
	}

	queryString, ok := args["query_string"].(string)
	if !ok || queryString == "" {
		return nil, goerr.New("query_string is required")
	}

	repository := "search-all"
	if repo, ok := args["repository"].(string); ok && repo != "" {
		repository = repo
	}

	body := map[string]any{"queryString": queryString}
	if start, ok := args["start"].(string); ok && start != "" {
		body["start"] = start
	} else {
		body["start"] = "1d"
	}
	if end, ok := args["end"].(string); ok && end != "" {
		body["end"] = end
	} else {
		body["end"] = "now"
	}

	// Step 1: create the query job.
	jobPath := fmt.Sprintf("/humio/api/v1/repositories/%s/queryjobs", repository)
	jobResp, err := t.doRequest(ctx, http.MethodPost, jobPath, body)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create event search query job",
			goerr.V("repository", repository),
			goerr.V("query", queryString),
		)
	}
	jobID, ok := jobResp["id"].(string)
	if !ok || jobID == "" {
		return nil, goerr.New("no job ID returned from query job creation", goerr.V("response", jobResp))
	}

	// Step 2: poll until done or the fetch cap is reached.
	resultPath := fmt.Sprintf("/humio/api/v1/repositories/%s/queryjobs/%s", repository, jobID)
	var allEvents []any
	done := false
	capHit := false
	var metadata any

	for i := range eventsMaxPolls {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, goerr.Wrap(ctx.Err(), "context canceled while polling event search")
			case <-time.After(eventsPollInterval):
			}
		}

		pollResp, err := t.doRequest(ctx, http.MethodGet, resultPath, nil)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to poll event search results",
				goerr.V("job_id", jobID),
				goerr.V("poll_attempt", i+1),
			)
		}
		if events, ok := pollResp["events"].([]any); ok {
			allEvents = append(allEvents, events...)
		}
		if d, ok := pollResp["done"].(bool); ok && d {
			done = true
			if meta, ok := pollResp["metadataResult"]; ok {
				metadata = meta
			}
			break
		}
		if len(allEvents) >= t.maxFetchRecords {
			capHit = true
			break
		}
	}

	// truncated means more events exist upstream than were returned: we either
	// stopped at the fetch cap, or the job finished with more than the cap.
	// pollExhausted means we ran out of polls before the job completed.
	fetched := len(allEvents)
	truncated := capHit || fetched > t.maxFetchRecords
	pollExhausted := !done && !capHit
	if fetched > t.maxFetchRecords {
		allEvents = allEvents[:t.maxFetchRecords]
	}

	page, token, hasMore, err := t.paginate(allEvents, t.maxRecords)
	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"records":    page,
		"count":      len(page),
		"total":      len(allEvents),
		"has_more":   hasMore,
		"truncated":  truncated,
		"repository": repository,
		"done":       done,
	}
	if hasMore {
		result["page_token"] = token
	}
	if metadata != nil {
		result["metadata"] = metadata
	}
	// NGSIEM does not return a grand total, so total_available is omitted; the
	// note communicates that more matched than was retrieved.
	note := searchNote(len(page), len(allEvents), hasMore, truncated, 0)
	if pollExhausted {
		t.logger.Warn("event search did not complete within poll limit",
			slog.String("job_id", jobID),
			slog.Int("total_events", len(allEvents)),
		)
		note += " The search job did not complete within the polling limit; these are partial results."
	}
	result["note"] = note

	return result, nil
}
