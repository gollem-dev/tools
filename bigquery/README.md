# bigquery

Run Google BigQuery queries and inspect datasets, table schemas, and SQL
runbooks, for the [gollem](https://github.com/gollem-dev/gollem) LLM agent
framework.

```
github.com/gollem-dev/tools/bigquery
```

## Tools

| Name | Description |
|------|-------------|
| `bigquery_list_dataset` | List available BigQuery datasets, tables, and partial schema. |
| `bigquery_query` | Execute a SQL query with scan-limit enforcement. |
| `bigquery_result` | Get the results of a previously executed query. |
| `bigquery_table_summary` | Get a summary of available BigQuery tables. |
| `bigquery_schema` | Get the detailed schema for a specific table. |
| `get_runbook_entry` | Get a SQL runbook entry by ID. |

`bigquery_query` writes results to a GCS bucket and `bigquery_result` reads them
back, so those two tools require `WithStorageBucket`. Queries are capped by
`WithScanLimit` (bytes scanned) to guard against runaway costs.

## Usage

```go
ts, err := bigquery.New(
	bigquery.WithProjectID("my-project"),
	bigquery.WithStorageBucket("my-result-bucket"), // needed for query/result
)
if err != nil {
	return err
}
if err := ts.Ping(ctx); err != nil { // optional preflight
	return err
}
```

Credentials come from Application Default Credentials unless overridden with
`WithCredentials` or `WithImpersonateServiceAccount`.

## Options

| Option | Required | Default |
|--------|----------|---------|
| `WithProjectID(string)` | yes | — |
| `WithStorageBucket(string)` | for `query`/`result` | — |
| `WithStoragePrefix(string)` | no | — |
| `WithCredentials(string)` | no | Application Default Credentials |
| `WithImpersonateServiceAccount(string)` | no | — |
| `WithConfigFiles([]string)` | no | — |
| `WithRunbookPaths([]string)` | no | — |
| `WithTimeout(time.Duration)` | no | `5m` |
| `WithScanLimit(string)` | no | `"10GB"` |
| `WithLogger(*slog.Logger)` | no | `slog.Default()` |

## Testing

Mock tests run unconditionally. The live-service test runs only when the
`TEST_BIGQUERY_*` variables are set:

```sh
TEST_BIGQUERY_PROJECT_ID=... TEST_BIGQUERY_STORAGE_BUCKET=... \
	TEST_BIGQUERY_DATASET=... TEST_BIGQUERY_TABLE=... \
	TEST_BIGQUERY_QUERY="SELECT 1" go test ./...
```

Optional: `TEST_BIGQUERY_CREDENTIALS`, `TEST_BIGQUERY_CONFIG`.
