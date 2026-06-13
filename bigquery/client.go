package bigquery

import (
	"context"

	"cloud.google.com/go/bigquery"
	"google.golang.org/api/option"
)

// bigQueryClient is an interface wrapping the subset of *bigquery.Client used
// by this package. Keeping it unexported prevents external callers from
// depending on the interface shape.
type bigQueryClient interface {
	Query(query string) bigQueryQuery
	Dataset(datasetID string) bigQueryDataset
	Close() error
}

// bigQueryQuery abstracts *bigquery.Query for testability.
type bigQueryQuery interface {
	Run(ctx context.Context) (bigQueryJob, error)
	SetDryRun(dryRun bool)
}

// bigQueryJob abstracts *bigquery.Job for testability.
type bigQueryJob interface {
	Wait(ctx context.Context) (*bigquery.JobStatus, error)
	Read(ctx context.Context) (bigQueryRowIterator, error)
	LastStatus() *bigquery.JobStatus
	ID() string
	Location() string
}

// bigQueryRowIterator abstracts *bigquery.RowIterator for testability.
type bigQueryRowIterator interface {
	Next(dst any) error
	Schema() bigquery.Schema
}

// bigQueryDataset abstracts *bigquery.Dataset for testability.
type bigQueryDataset interface {
	Table(tableID string) bigQueryTable
}

// bigQueryTable abstracts *bigquery.Table for testability.
type bigQueryTable interface {
	Metadata(ctx context.Context) (*bigquery.TableMetadata, error)
}

// bigQueryClientFactory creates bigQueryClient instances. Abstracting creation
// allows tests to inject a mock factory without network calls.
type bigQueryClientFactory interface {
	NewClient(ctx context.Context, projectID string, opts ...option.ClientOption) (bigQueryClient, error)
}

// defaultBigQueryClientFactory is the production factory that creates real BQ clients.
type defaultBigQueryClientFactory struct{}

var _ bigQueryClientFactory = (*defaultBigQueryClientFactory)(nil)

func (f *defaultBigQueryClientFactory) NewClient(ctx context.Context, projectID string, opts ...option.ClientOption) (bigQueryClient, error) {
	client, err := bigquery.NewClient(ctx, projectID, opts...)
	if err != nil {
		return nil, err
	}
	return &defaultBigQueryClient{client: client}, nil
}

// defaultBigQueryClient wraps the real *bigquery.Client.
type defaultBigQueryClient struct {
	client *bigquery.Client
}

var _ bigQueryClient = (*defaultBigQueryClient)(nil)

func (c *defaultBigQueryClient) Query(query string) bigQueryQuery {
	return &defaultBigQueryQuery{query: c.client.Query(query)}
}

func (c *defaultBigQueryClient) Dataset(datasetID string) bigQueryDataset {
	return &defaultBigQueryDataset{dataset: c.client.Dataset(datasetID)}
}

func (c *defaultBigQueryClient) Close() error {
	return c.client.Close()
}

// defaultBigQueryQuery wraps the real *bigquery.Query.
type defaultBigQueryQuery struct {
	query *bigquery.Query
}

var _ bigQueryQuery = (*defaultBigQueryQuery)(nil)

func (q *defaultBigQueryQuery) Run(ctx context.Context) (bigQueryJob, error) {
	job, err := q.query.Run(ctx)
	if err != nil {
		return nil, err
	}
	return &defaultBigQueryJob{job: job}, nil
}

func (q *defaultBigQueryQuery) SetDryRun(dryRun bool) {
	q.query.DryRun = dryRun
}

// defaultBigQueryJob wraps the real *bigquery.Job.
type defaultBigQueryJob struct {
	job *bigquery.Job
}

var _ bigQueryJob = (*defaultBigQueryJob)(nil)

func (j *defaultBigQueryJob) Wait(ctx context.Context) (*bigquery.JobStatus, error) {
	return j.job.Wait(ctx)
}

func (j *defaultBigQueryJob) Read(ctx context.Context) (bigQueryRowIterator, error) {
	iter, err := j.job.Read(ctx)
	if err != nil {
		return nil, err
	}
	return &defaultBigQueryRowIterator{iter: iter}, nil
}

func (j *defaultBigQueryJob) LastStatus() *bigquery.JobStatus {
	return j.job.LastStatus()
}

func (j *defaultBigQueryJob) ID() string {
	return j.job.ID()
}

func (j *defaultBigQueryJob) Location() string {
	return j.job.Location()
}

// defaultBigQueryRowIterator wraps the real *bigquery.RowIterator.
type defaultBigQueryRowIterator struct {
	iter *bigquery.RowIterator
}

var _ bigQueryRowIterator = (*defaultBigQueryRowIterator)(nil)

func (r *defaultBigQueryRowIterator) Next(dst any) error {
	return r.iter.Next(dst)
}

func (r *defaultBigQueryRowIterator) Schema() bigquery.Schema {
	return r.iter.Schema
}

// defaultBigQueryDataset wraps the real *bigquery.Dataset.
type defaultBigQueryDataset struct {
	dataset *bigquery.Dataset
}

var _ bigQueryDataset = (*defaultBigQueryDataset)(nil)

func (d *defaultBigQueryDataset) Table(tableID string) bigQueryTable {
	return &defaultBigQueryTable{table: d.dataset.Table(tableID)}
}

// defaultBigQueryTable wraps the real *bigquery.Table.
type defaultBigQueryTable struct {
	table *bigquery.Table
}

var _ bigQueryTable = (*defaultBigQueryTable)(nil)

func (t *defaultBigQueryTable) Metadata(ctx context.Context) (*bigquery.TableMetadata, error) {
	return t.table.Metadata(ctx)
}
