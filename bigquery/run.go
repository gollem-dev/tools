package bigquery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"cloud.google.com/go/bigquery"
	"github.com/gollem-dev/tools/internal/safe"
	"github.com/google/uuid"
	"github.com/m-mizutani/goerr/v2"
	"google.golang.org/api/iterator"
)

// storageFor returns the configured storageBackend. When a test-injected backend
// is present it is returned directly; otherwise a real GCS client is created.
func (t *ToolSet) storageFor(ctx context.Context) (storageBackend, func(), error) {
	if t.storage != nil {
		return t.storage, func() {}, nil
	}
	client, err := t.newStorageClient(ctx)
	if err != nil {
		return nil, nil, goerr.Wrap(err, "failed to create GCS storage client")
	}
	backend := &gcsStorageBackend{client: client, logger: t.logger}
	cleanup := func() { safe.Close(t.logger, client) }
	return backend, cleanup, nil
}

// runWithClient executes the named tool that requires a live BigQuery client.
func (t *ToolSet) runWithClient(ctx context.Context, name string, args map[string]any, client bigQueryClient) (map[string]any, error) {
	switch name {
	case "bigquery_list_dataset":
		if len(t.configs) == 0 {
			return nil, goerr.New("no BigQuery configuration loaded; use WithConfigFiles")
		}
		return t.listDatasets()

	case "bigquery_query":
		if len(t.configs) == 0 {
			return nil, goerr.New("no BigQuery configuration loaded; use WithConfigFiles")
		}
		query, ok := args["query"].(string)
		if !ok || query == "" {
			return nil, goerr.New("query parameter is required")
		}
		return t.executeQuery(ctx, client, query)

	case "bigquery_result":
		if len(t.configs) == 0 {
			return nil, goerr.New("no BigQuery configuration loaded; use WithConfigFiles")
		}
		queryID, ok := args["query_id"].(string)
		if !ok || queryID == "" {
			return nil, goerr.New("query_id parameter is required")
		}
		limit := 100
		if v, ok := args["limit"].(float64); ok {
			limit = int(v)
		} else if args["limit"] != nil {
			return nil, goerr.New("invalid limit parameter type",
				goerr.V("type", fmt.Sprintf("%T", args["limit"])),
				goerr.V("value", args["limit"]))
		}
		offset := 0
		if v, ok := args["offset"].(float64); ok {
			offset = int(v)
		} else if args["offset"] != nil {
			return nil, goerr.New("invalid offset parameter type",
				goerr.V("type", fmt.Sprintf("%T", args["offset"])),
				goerr.V("value", args["offset"]))
		}
		return t.getQueryResults(ctx, client, queryID, limit, offset)

	case "bigquery_schema":
		projectID, ok := args["project_id"].(string)
		if !ok || projectID == "" {
			return nil, goerr.New("project_id parameter is required")
		}
		datasetID, ok := args["dataset_id"].(string)
		if !ok || datasetID == "" {
			return nil, goerr.New("dataset_id parameter is required")
		}
		tableID, ok := args["table_id"].(string)
		if !ok || tableID == "" {
			return nil, goerr.New("table_id parameter is required")
		}
		// When the requested project matches the default, reuse the already-open
		// client to avoid creating a redundant connection.
		if projectID == t.projectID {
			return t.getTableSchemaWithClient(ctx, client, datasetID, tableID)
		}
		return t.getTableSchema(ctx, projectID, datasetID, tableID)

	default:
		return nil, goerr.New("invalid function name", goerr.V("name", name))
	}
}

// listDatasets returns the in-memory table configuration as JSON-safe maps.
func (t *ToolSet) listDatasets() (map[string]any, error) {
	raw, err := json.Marshal(t.configs)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to marshal configs")
	}
	var result []map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, goerr.Wrap(err, "failed to unmarshal configs")
	}
	return map[string]any{"config": result}, nil
}

// toResultStoragePath returns the GCS object path for query result data.
func (t *ToolSet) toResultStoragePath(queryID string) string {
	return fmt.Sprintf("%sbigquery/%s/data.json", t.storagePrefix, queryID)
}

// toMetadataStoragePath returns the GCS object path for query job metadata.
func (t *ToolSet) toMetadataStoragePath(queryID string) string {
	return fmt.Sprintf("%sbigquery/%s/metadata.json", t.storagePrefix, queryID)
}

// executeQuery performs a dry-run scan check, submits the real query, then
// writes job metadata to GCS. The caller uses bigquery_result to retrieve rows.
// A storage bucket must be configured (WithStorageBucket) for this operation.
func (t *ToolSet) executeQuery(ctx context.Context, client bigQueryClient, query string) (map[string]any, error) {
	if t.storageBucket == "" && t.storage == nil {
		return nil, goerr.New("storage bucket is required for bigquery_query; use WithStorageBucket")
	}
	t.logger.Debug("executing BigQuery query", slog.String("query", query))

	// Dry run to check scan size.
	dryQ := client.Query(query)
	dryQ.SetDryRun(true)
	dryJob, err := dryQ.Run(ctx)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to dry-run query")
	}

	totalBytes := dryJob.LastStatus().Statistics.TotalBytesProcessed
	if totalBytes < 0 {
		return nil, goerr.New("invalid negative bytes processed",
			goerr.V("bytes_processed", totalBytes))
	}
	if totalBytes > 0 && uint64(totalBytes) > t.scanLimit {
		return nil, goerr.New("query scan size exceeds limit",
			goerr.V("scan_bytes", totalBytes),
			goerr.V("scan_limit", t.scanLimit))
	}

	// Execute the real query.
	realQ := client.Query(query)
	realQ.SetDryRun(false)
	job, err := realQ.Run(ctx)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to run query")
	}

	store, cleanup, err := t.storageFor(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	queryID := uuid.New().String()
	meta := queryMetadata{
		JobID:    job.ID(),
		Location: job.Location(),
	}

	metaBytes, err := encodeMetadata(meta)
	if err != nil {
		return nil, err
	}

	if err := store.WriteObject(ctx, t.storageBucket, t.toMetadataStoragePath(queryID), metaBytes); err != nil {
		return nil, goerr.Wrap(err, "failed to write job metadata to storage")
	}

	return map[string]any{"query_id": queryID}, nil
}

// getQueryResults reads results for a previously submitted query. If the result
// data file already exists in GCS it is read directly; otherwise the job is
// polled until completion and the rows are written to GCS.
func (t *ToolSet) getQueryResults(ctx context.Context, client bigQueryClient, queryID string, limit, offset int) (map[string]any, error) {
	store, cleanup, err := t.storageFor(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	metaBytes, err := store.ReadObject(ctx, t.storageBucket, t.toMetadataStoragePath(queryID))
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read query metadata from storage",
			goerr.V("query_id", queryID))
	}

	meta, err := decodeMetadata(metaBytes)
	if err != nil {
		return nil, err
	}

	resultPath := t.toResultStoragePath(queryID)

	// If result data file already exists, read it directly (cached).
	exists, err := store.ObjectExists(ctx, t.storageBucket, resultPath)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to check result file existence")
	}
	if exists {
		data, err := store.ReadObject(ctx, t.storageBucket, resultPath)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to read cached result file")
		}
		return t.processStorageResults(ctx, data, limit, offset)
	}

	// Result not yet cached — retrieve from the BigQuery job.
	job, err := t.jobFromIDLocation(ctx, client, meta.JobID, meta.Location)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to retrieve BigQuery job",
			goerr.V("job_id", meta.JobID))
	}

	waitCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	status, err := job.Wait(waitCtx)
	if err != nil {
		return nil, goerr.Wrap(err, "failed waiting for BigQuery job",
			goerr.V("job_id", meta.JobID))
	}
	if err := status.Err(); err != nil {
		return nil, goerr.Wrap(err, "BigQuery job completed with error",
			goerr.V("job_id", meta.JobID))
	}

	it, err := job.Read(ctx)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read BigQuery job results")
	}

	// Stream rows into an in-memory buffer, then persist to storage.
	result, rowData, err := t.processBigQueryResultsToBuffer(ctx, it, limit, offset)
	if err != nil {
		return nil, err
	}

	if err := store.WriteObject(ctx, t.storageBucket, resultPath, rowData); err != nil {
		return nil, goerr.Wrap(err, "failed to write result data to storage")
	}

	return result, nil
}

// jobLookup is an optional interface that mock clients can implement to support
// looking up a pre-registered job by its ID. This avoids the need for a real
// *bigquery.Client in tests.
type jobLookup interface {
	LookupJob(jobID string) (bigQueryJob, bool)
}

// jobFromIDLocation obtains a bigQueryJob handle from a job ID and location.
// It first checks whether the client implements jobLookup (used by mocks);
// otherwise it casts to the real implementation.
func (t *ToolSet) jobFromIDLocation(ctx context.Context, client bigQueryClient, jobID, location string) (bigQueryJob, error) {
	if jl, ok := client.(jobLookup); ok {
		job, found := jl.LookupJob(jobID)
		if found {
			return job, nil
		}
		return nil, goerr.New("mock job not found", goerr.V("job_id", jobID))
	}

	dc, ok := client.(*defaultBigQueryClient)
	if !ok {
		return nil, goerr.New("jobFromIDLocation: unsupported client type",
			goerr.V("job_id", jobID))
	}
	job, err := dc.client.JobFromIDLocation(ctx, jobID, location)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get job from ID/location",
			goerr.V("job_id", jobID), goerr.V("location", location))
	}
	return &defaultBigQueryJob{job: job}, nil
}

// rowProcessor abstracts reading and writing a single row during result
// streaming.
type rowProcessor interface {
	processRow() (map[string]bigquery.Value, error)
	writeRow(row map[string]bigquery.Value) error
}

type storageRowProcessor struct {
	decoder *json.Decoder
}

func (p *storageRowProcessor) processRow() (map[string]bigquery.Value, error) {
	var row map[string]bigquery.Value
	if !p.decoder.More() {
		return nil, iterator.Done
	}
	if err := p.decoder.Decode(&row); err != nil {
		return nil, goerr.Wrap(err, "failed to decode row from JSON")
	}
	return row, nil
}

func (p *storageRowProcessor) writeRow(_ map[string]bigquery.Value) error {
	// Already written during the initial query pass; no re-write needed.
	return nil
}

type bigQueryRowProcessor struct {
	iterator bigQueryRowIterator
	buf      *bytes.Buffer
}

func (p *bigQueryRowProcessor) processRow() (map[string]bigquery.Value, error) {
	var row []bigquery.Value
	if err := p.iterator.Next(&row); err != nil {
		if errors.Is(err, iterator.Done) {
			return nil, iterator.Done
		}
		return nil, goerr.Wrap(err, "failed to iterate results")
	}

	rowMap := make(map[string]bigquery.Value)
	schema := p.iterator.Schema()
	for i, field := range schema {
		if i < len(row) {
			rowMap[field.Name] = convertBigQueryValue(row[i])
		}
	}
	return rowMap, nil
}

func (p *bigQueryRowProcessor) writeRow(row map[string]bigquery.Value) error {
	if p.buf != nil {
		if err := json.NewEncoder(p.buf).Encode(row); err != nil {
			return goerr.Wrap(err, "failed to buffer row JSON")
		}
	}
	return nil
}

// processResults iterates processor rows, applying limit/offset, and returns a
// result map along with the raw JSON bytes of all processed rows.
func (t *ToolSet) processResults(_ context.Context, proc rowProcessor, limit, offset int) (map[string]any, error) {
	var rows []map[string]any
	var totalSize int64
	var totalRows int

	for {
		row, err := proc.processRow()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}

		// Persist EVERY row so the cached result holds the complete result set,
		// independent of the requested limit/offset window. (For the storage
		// processor this is a no-op since the data is already cached.) Writing
		// only the window here would corrupt later pages that read the cache.
		if err := proc.writeRow(row); err != nil {
			return nil, err
		}

		// Collect only the requested [offset, offset+limit) window for the
		// response, while totalRows keeps counting the full result set.
		if totalRows >= offset && len(rows) < limit {
			rowJSON, err := json.Marshal(row)
			if err != nil {
				return nil, goerr.Wrap(err, "failed to marshal row")
			}
			var rowMap map[string]any
			if err := json.Unmarshal(rowJSON, &rowMap); err != nil {
				return nil, goerr.Wrap(err, "failed to unmarshal row")
			}
			rows = append(rows, rowMap)
			totalSize += int64(len(rowJSON))
		}

		totalRows++
	}

	rowsJSON, err := json.Marshal(rows)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to marshal rows to JSON")
	}

	return map[string]any{
		"rows_json":  string(rowsJSON),
		"total_rows": totalRows,
		"total_size": totalSize,
		"limit":      limit,
		"offset":     offset,
		"has_more":   totalRows > offset+limit,
	}, nil
}

func (t *ToolSet) processStorageResults(ctx context.Context, data []byte, limit, offset int) (map[string]any, error) {
	proc := &storageRowProcessor{decoder: json.NewDecoder(bytes.NewReader(data))}
	return t.processResults(ctx, proc, limit, offset)
}

// processBigQueryResultsToBuffer streams BigQuery rows into a buffer and
// returns the result map and raw JSON bytes for storage.
func (t *ToolSet) processBigQueryResultsToBuffer(ctx context.Context, it bigQueryRowIterator, limit, offset int) (map[string]any, []byte, error) {
	var buf bytes.Buffer
	proc := &bigQueryRowProcessor{iterator: it, buf: &buf}
	result, err := t.processResults(ctx, proc, limit, offset)
	if err != nil {
		return nil, nil, err
	}
	return result, buf.Bytes(), nil
}

// getTableSchemaWithClient fetches and returns schema using an already-open
// client.
func (t *ToolSet) getTableSchemaWithClient(ctx context.Context, client bigQueryClient, datasetID, tableID string) (map[string]any, error) {
	metadata, err := client.Dataset(datasetID).Table(tableID).Metadata(ctx)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get table metadata",
			goerr.V("dataset_id", datasetID),
			goerr.V("table_id", tableID))
	}
	return marshalSchema(metadata)
}

// getTableSchema creates a new BQ client for projectID and fetches the schema.
// Use this when the projectID differs from t.projectID.
func (t *ToolSet) getTableSchema(ctx context.Context, projectID, datasetID, tableID string) (map[string]any, error) {
	opts, err := t.clientOptions(ctx)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to build client options for schema fetch")
	}
	client, err := t.clientFactory.NewClient(ctx, projectID, opts...)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create BigQuery client for schema fetch",
			goerr.V("project_id", projectID))
	}
	defer safe.Close(t.logger, client)

	metadata, err := client.Dataset(datasetID).Table(tableID).Metadata(ctx)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get table metadata",
			goerr.V("project_id", projectID),
			goerr.V("dataset_id", datasetID),
			goerr.V("table_id", tableID))
	}
	return marshalSchema(metadata)
}

func marshalSchema(metadata *bigquery.TableMetadata) (map[string]any, error) {
	schemaJSON, err := json.Marshal(metadata.Schema)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to marshal schema")
	}
	var result []map[string]any
	if err := json.Unmarshal(schemaJSON, &result); err != nil {
		return nil, goerr.Wrap(err, "failed to unmarshal schema")
	}
	return map[string]any{"schema": result}, nil
}

// getTableSummary returns summary information for configured tables matching the
// optional filter arguments.
func (t *ToolSet) getTableSummary(projectID, datasetID, tableID string) (map[string]any, error) {
	var results []map[string]any

	for _, cfg := range t.configs {
		configProjectID := t.projectID
		if projectID != "" && configProjectID != projectID {
			continue
		}
		if datasetID != "" && cfg.DatasetID != datasetID {
			continue
		}
		if tableID != "" && cfg.TableID != tableID {
			continue
		}

		var columnSummaries []map[string]any
		for _, col := range cfg.Columns {
			colSummary := map[string]any{
				"name": col.Name,
				"type": col.Type,
			}
			if col.Description != "" {
				colSummary["description"] = col.Description
			}
			if col.ValueExample != "" {
				colSummary["value_example"] = col.ValueExample
			}
			if len(col.Fields) > 0 {
				colSummary["has_nested_fields"] = true
				colSummary["nested_fields_count"] = len(col.Fields)
			}
			columnSummaries = append(columnSummaries, colSummary)
		}

		tableSummary := map[string]any{
			"project_id": configProjectID,
			"dataset_id": cfg.DatasetID,
			"table_id":   cfg.TableID,
			"columns":    columnSummaries,
		}
		if cfg.Description != "" {
			tableSummary["description"] = cfg.Description
		}
		if cfg.Partitioning.Field != "" {
			tableSummary["partitioning"] = map[string]any{
				"field":     cfg.Partitioning.Field,
				"type":      cfg.Partitioning.Type,
				"time_unit": cfg.Partitioning.TimeUnit,
			}
		}

		results = append(results, tableSummary)
	}

	return map[string]any{
		"tables": results,
		"total":  len(results),
	}, nil
}

// getRunbookEntry retrieves a runbook entry by its string ID.
func (t *ToolSet) getRunbookEntry(runbookID string) (map[string]any, error) {
	entry, ok := t.runbooks[RunbookID(runbookID)]
	if !ok {
		return nil, goerr.New("runbook entry not found",
			goerr.V("runbook_id", runbookID))
	}
	return map[string]any{
		"id":          entry.ID.String(),
		"title":       entry.Title,
		"description": entry.Description,
		"sql_content": entry.SQLContent,
	}, nil
}
