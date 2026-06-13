package bigquery

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"cloud.google.com/go/bigquery"
	"github.com/m-mizutani/goerr/v2"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// mockBigQueryClientFactory is a test implementation of bigQueryClientFactory.
type mockBigQueryClientFactory struct {
	Client bigQueryClient
}

var _ bigQueryClientFactory = (*mockBigQueryClientFactory)(nil)

func (f *mockBigQueryClientFactory) NewClient(_ context.Context, _ string, _ ...option.ClientOption) (bigQueryClient, error) {
	return f.Client, nil
}

// mockBigQueryClient is a test implementation of bigQueryClient.
type mockBigQueryClient struct {
	// TableMetadata maps "datasetID.tableID" to metadata.
	TableMetadata map[string]*bigquery.TableMetadata
	// QueryResults maps query SQL to row slices.
	QueryResults map[string][]map[string]any
	// Errors maps operation name to error.
	Errors map[string]error
	// ExecutedQueries records all query strings that were Run.
	ExecutedQueries []string
	// DryRunResults maps query SQL to statistics for dry-run jobs.
	DryRunResults map[string]*bigquery.JobStatistics
	// JobsByID maps jobID to a pre-built job for use by jobFromIDLocation.
	JobsByID map[string]bigQueryJob
}

var _ bigQueryClient = (*mockBigQueryClient)(nil)

func newMockBigQueryClient() *mockBigQueryClient {
	return &mockBigQueryClient{
		TableMetadata:   make(map[string]*bigquery.TableMetadata),
		QueryResults:    make(map[string][]map[string]any),
		Errors:          make(map[string]error),
		ExecutedQueries: make([]string, 0),
		DryRunResults:   make(map[string]*bigquery.JobStatistics),
		JobsByID:        make(map[string]bigQueryJob),
	}
}

func (c *mockBigQueryClient) Query(query string) bigQueryQuery {
	return &mockBigQueryQuery{client: c, query: query}
}

func (c *mockBigQueryClient) Dataset(datasetID string) bigQueryDataset {
	return &mockBigQueryDataset{client: c, datasetID: datasetID}
}

func (c *mockBigQueryClient) Close() error {
	if err, ok := c.Errors["close"]; ok {
		return err
	}
	return nil
}

// LookupJob implements jobLookup to support jobFromIDLocation in tests.
func (c *mockBigQueryClient) LookupJob(jobID string) (bigQueryJob, bool) {
	job, ok := c.JobsByID[jobID]
	return job, ok
}

// mockBigQueryQuery is a test implementation of bigQueryQuery.
type mockBigQueryQuery struct {
	client *mockBigQueryClient
	query  string
	dryRun bool
}

var _ bigQueryQuery = (*mockBigQueryQuery)(nil)

func (q *mockBigQueryQuery) Run(ctx context.Context) (bigQueryJob, error) {
	if err, ok := q.client.Errors["query_run"]; ok {
		return nil, err
	}
	q.client.ExecutedQueries = append(q.client.ExecutedQueries, q.query)
	job := &mockBigQueryJob{
		client: q.client,
		query:  q.query,
		dryRun: q.dryRun,
		id:     fmt.Sprintf("mock-job-%d", len(q.client.ExecutedQueries)),
	}
	// Register the job so jobFromIDLocation -> LookupJob can find it later when
	// bigquery_result reads from the job (uncached) path.
	q.client.JobsByID[job.id] = job
	return job, nil
}

func (q *mockBigQueryQuery) SetDryRun(dryRun bool) {
	q.dryRun = dryRun
}

// mockBigQueryJob is a test implementation of bigQueryJob.
type mockBigQueryJob struct {
	client *mockBigQueryClient
	query  string
	dryRun bool
	id     string
}

var _ bigQueryJob = (*mockBigQueryJob)(nil)

func (j *mockBigQueryJob) Wait(_ context.Context) (*bigquery.JobStatus, error) {
	if err, ok := j.client.Errors["job_wait"]; ok {
		return nil, err
	}
	status := &bigquery.JobStatus{State: bigquery.Done}
	if j.dryRun {
		if stats, ok := j.client.DryRunResults[j.query]; ok {
			status.Statistics = stats
		} else {
			status.Statistics = &bigquery.JobStatistics{TotalBytesProcessed: 1000}
		}
	}
	return status, nil
}

func (j *mockBigQueryJob) Read(_ context.Context) (bigQueryRowIterator, error) {
	if err, ok := j.client.Errors["job_read"]; ok {
		return nil, err
	}
	if j.dryRun {
		return nil, goerr.New("cannot read from a dry-run job")
	}
	var rows []map[string]any
	if result, ok := j.client.QueryResults[j.query]; ok {
		rows = result
	}
	return &mockBigQueryRowIterator{rows: rows, schema: schemaFromRows(rows)}, nil
}

// schemaFromRows derives a deterministic schema from the first row's keys so
// that the job-read code path produces non-empty rows in tests. Field types are
// not significant for the result processing (only names are used).
func schemaFromRows(rows []map[string]any) bigquery.Schema {
	if len(rows) == 0 {
		return nil
	}
	keys := make([]string, 0, len(rows[0]))
	for k := range rows[0] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	schema := make(bigquery.Schema, 0, len(keys))
	for _, k := range keys {
		schema = append(schema, &bigquery.FieldSchema{Name: k, Type: bigquery.StringFieldType})
	}
	return schema
}

func (j *mockBigQueryJob) LastStatus() *bigquery.JobStatus {
	status := &bigquery.JobStatus{State: bigquery.Done}
	if j.dryRun {
		if stats, ok := j.client.DryRunResults[j.query]; ok {
			status.Statistics = stats
		} else {
			status.Statistics = &bigquery.JobStatistics{TotalBytesProcessed: 1000}
		}
	}
	return status
}

func (j *mockBigQueryJob) ID() string {
	return j.id
}

func (j *mockBigQueryJob) Location() string {
	return "US"
}

// mockBigQueryRowIterator is a test implementation of bigQueryRowIterator.
type mockBigQueryRowIterator struct {
	rows   []map[string]any
	index  int
	schema bigquery.Schema
}

var _ bigQueryRowIterator = (*mockBigQueryRowIterator)(nil)

func (r *mockBigQueryRowIterator) Next(dst interface{}) error {
	if r.index >= len(r.rows) {
		return iterator.Done
	}
	row := r.rows[r.index]
	r.index++

	switch v := dst.(type) {
	case *[]bigquery.Value:
		// Build a parallel value slice that matches the schema order.
		values := make([]bigquery.Value, 0, len(r.schema))
		if len(r.schema) > 0 {
			for _, field := range r.schema {
				values = append(values, row[field.Name])
			}
		} else {
			for _, val := range row {
				values = append(values, val)
			}
		}
		*v = values
	default:
		data, err := json.Marshal(row)
		if err != nil {
			return goerr.Wrap(err, "mock: failed to marshal row")
		}
		if err := json.Unmarshal(data, dst); err != nil {
			return goerr.Wrap(err, "mock: failed to unmarshal row")
		}
	}
	return nil
}

func (r *mockBigQueryRowIterator) Schema() bigquery.Schema {
	return r.schema
}

// mockBigQueryDataset is a test implementation of bigQueryDataset.
type mockBigQueryDataset struct {
	client    *mockBigQueryClient
	datasetID string
}

var _ bigQueryDataset = (*mockBigQueryDataset)(nil)

func (d *mockBigQueryDataset) Table(tableID string) bigQueryTable {
	return &mockBigQueryTable{client: d.client, datasetID: d.datasetID, tableID: tableID}
}

// mockBigQueryTable is a test implementation of bigQueryTable.
type mockBigQueryTable struct {
	client    *mockBigQueryClient
	datasetID string
	tableID   string
}

var _ bigQueryTable = (*mockBigQueryTable)(nil)

func (t *mockBigQueryTable) Metadata(_ context.Context) (*bigquery.TableMetadata, error) {
	if err, ok := t.client.Errors["table_metadata"]; ok {
		return nil, err
	}
	key := fmt.Sprintf("%s.%s", t.datasetID, t.tableID)
	if metadata, ok := t.client.TableMetadata[key]; ok {
		return metadata, nil
	}
	// Default schema when no explicit metadata is configured.
	return &bigquery.TableMetadata{
		Schema: bigquery.Schema{
			{Name: "id", Type: bigquery.StringFieldType},
			{Name: "name", Type: bigquery.StringFieldType},
			{Name: "timestamp", Type: bigquery.TimestampFieldType},
		},
	}, nil
}
