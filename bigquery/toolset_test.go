package bigquery_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"cloud.google.com/go/bigquery"
	bqtool "github.com/gollem-dev/tools/bigquery"
	"github.com/m-mizutani/gt"
)

// writeConfigFile writes a YAML table config file to dir and returns its path.
func writeConfigFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	gt.NoError(t, os.WriteFile(path, []byte(content), 0600))
	return path
}

// minimalConfigYAML is a YAML config for a single table used in most tests.
const minimalConfigYAML = `
dataset_id: my_dataset
table_id: my_table
description: Test table
columns:
  - name: id
    type: STRING
    description: Row ID
  - name: value
    type: INTEGER
    description: Some integer value
`

// ---------------------------------------------------------------------------
// Construction tests
// ---------------------------------------------------------------------------

func TestNew_MissingProjectID(t *testing.T) {
	_, err := bqtool.New()
	gt.Error(t, err)
}

func TestNew_ScanLimitParseError(t *testing.T) {
	_, err := bqtool.New(
		bqtool.WithProjectID("my-project"),
		bqtool.WithScanLimit("not-a-number"),
	)
	gt.Error(t, err)
}

func TestNew_WithConfigFiles(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfigFile(t, dir, "table.yaml", minimalConfigYAML)

	ts := gt.R1(bqtool.New(
		bqtool.WithProjectID("my-project"),
		bqtool.WithConfigFiles([]string{cfgPath}),
	)).NoError(t)
	gt.NotNil(t, ts)
}

func TestNew_WithConfigDir(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "table.yaml", minimalConfigYAML)

	ts := gt.R1(bqtool.New(
		bqtool.WithProjectID("my-project"),
		bqtool.WithConfigFiles([]string{dir}),
	)).NoError(t)
	gt.NotNil(t, ts)
}

// ---------------------------------------------------------------------------
// Specs test
// ---------------------------------------------------------------------------

func TestSpecs_SixTools(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfigFile(t, dir, "table.yaml", minimalConfigYAML)

	ts := gt.R1(bqtool.New(
		bqtool.WithProjectID("my-project"),
		bqtool.WithConfigFiles([]string{cfgPath}),
	)).NoError(t)

	specs := gt.R1(ts.Specs(context.Background())).NoError(t)
	gt.Array(t, specs).Length(6)

	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
	}
	gt.Array(t, names).
		Has("bigquery_list_dataset").
		Has("bigquery_query").
		Has("bigquery_result").
		Has("bigquery_table_summary").
		Has("bigquery_schema").
		Has("get_runbook_entry")
}

// ---------------------------------------------------------------------------
// bigquery_list_dataset
// ---------------------------------------------------------------------------

func TestRun_ListDataset(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfigFile(t, dir, "table.yaml", minimalConfigYAML)

	mockClient := bqtool.NewMockClient()
	ts := gt.R1(bqtool.New(
		bqtool.WithProjectID("my-project"),
		bqtool.WithConfigFiles([]string{cfgPath}),
		bqtool.WithClientFactoryForTest(&bqtool.MockClientFactory{Client: mockClient}),
	)).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "bigquery_list_dataset", nil)).NoError(t)
	configs, ok := result["config"].([]map[string]any)
	gt.Bool(t, ok).True()
	gt.Array(t, configs).Length(1)
	gt.Map(t, configs[0]).HasKeyValue("dataset_id", "my_dataset")
	gt.Map(t, configs[0]).HasKeyValue("table_id", "my_table")
}

// ---------------------------------------------------------------------------
// bigquery_schema
// ---------------------------------------------------------------------------

func TestRun_Schema(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfigFile(t, dir, "table.yaml", minimalConfigYAML)

	mockClient := bqtool.NewMockClient()
	mockClient.TableMetadata["ds.tbl"] = &bigquery.TableMetadata{
		Schema: bigquery.Schema{
			{Name: "col1", Type: bigquery.StringFieldType},
		},
	}

	ts := gt.R1(bqtool.New(
		bqtool.WithProjectID("my-project"),
		bqtool.WithConfigFiles([]string{cfgPath}),
		bqtool.WithClientFactoryForTest(&bqtool.MockClientFactory{Client: mockClient}),
	)).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "bigquery_schema", map[string]any{
		"project_id": "my-project",
		"dataset_id": "ds",
		"table_id":   "tbl",
	})).NoError(t)
	gt.Map(t, result).HasKey("schema")
}

func TestRun_Schema_MissingProjectID(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfigFile(t, dir, "table.yaml", minimalConfigYAML)

	ts := gt.R1(bqtool.New(
		bqtool.WithProjectID("my-project"),
		bqtool.WithConfigFiles([]string{cfgPath}),
		bqtool.WithClientFactoryForTest(&bqtool.MockClientFactory{Client: bqtool.NewMockClient()}),
	)).NoError(t)

	_, err := ts.Run(context.Background(), "bigquery_schema", map[string]any{
		"dataset_id": "ds",
		"table_id":   "tbl",
		// project_id intentionally omitted
	})
	gt.Error(t, err)
}

// ---------------------------------------------------------------------------
// bigquery_table_summary
// ---------------------------------------------------------------------------

func TestRun_TableSummary(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfigFile(t, dir, "table.yaml", minimalConfigYAML)

	ts := gt.R1(bqtool.New(
		bqtool.WithProjectID("my-project"),
		bqtool.WithConfigFiles([]string{cfgPath}),
	)).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "bigquery_table_summary", map[string]any{})).NoError(t)

	tables, ok := result["tables"].([]map[string]any)
	gt.Bool(t, ok).True()
	gt.Array(t, tables).Length(1)
	gt.Map(t, tables[0]).HasKeyValue("project_id", "my-project")
	gt.Map(t, tables[0]).HasKeyValue("dataset_id", "my_dataset")
	gt.Map(t, tables[0]).HasKeyValue("table_id", "my_table")
	gt.Map(t, tables[0]).HasKeyValue("description", "Test table")
}

func TestRun_TableSummary_WithFilter(t *testing.T) {
	dir := t.TempDir()

	// Write two config files for different datasets.
	writeConfigFile(t, dir, "a.yaml", "dataset_id: ds_a\ntable_id: tbl_a\n")
	writeConfigFile(t, dir, "b.yaml", "dataset_id: ds_b\ntable_id: tbl_b\n")

	ts := gt.R1(bqtool.New(
		bqtool.WithProjectID("my-project"),
		bqtool.WithConfigFiles([]string{dir}),
	)).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "bigquery_table_summary", map[string]any{
		"dataset_id": "ds_a",
	})).NoError(t)

	tables, ok := result["tables"].([]map[string]any)
	gt.Bool(t, ok).True()
	gt.Array(t, tables).Length(1)
	gt.Map(t, tables[0]).HasKeyValue("dataset_id", "ds_a")
}

// ---------------------------------------------------------------------------
// get_runbook_entry
// ---------------------------------------------------------------------------

func TestRun_GetRunbookEntry(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfigFile(t, dir, "table.yaml", minimalConfigYAML)

	// Create a runbook SQL file with embedded title and description.
	sqlContent := "-- Title: Find large events\n-- Description: Returns the top 10 events by size.\nSELECT id, size FROM my_dataset.my_table ORDER BY size DESC LIMIT 10;\n"
	sqlPath := filepath.Join(dir, "query.sql")
	gt.NoError(t, os.WriteFile(sqlPath, []byte(sqlContent), 0600))

	ts := gt.R1(bqtool.New(
		bqtool.WithProjectID("my-project"),
		bqtool.WithConfigFiles([]string{cfgPath}),
		bqtool.WithRunbookPaths([]string{sqlPath}),
	)).NoError(t)

	// Confirm invalid ID returns an error.
	_, err := ts.Run(context.Background(), "get_runbook_entry", map[string]any{
		"runbook_id": "non-existent-id",
	})
	gt.Error(t, err)

	// Retrieve a valid ID via the test-only helper and verify the entry.
	ids := bqtool.RunbookIDsForTest(ts)
	gt.Array(t, ids).Length(1)

	result := gt.R1(ts.Run(context.Background(), "get_runbook_entry", map[string]any{
		"runbook_id": ids[0],
	})).NoError(t)
	gt.Map(t, result).HasKeyValue("title", "Find large events")
	gt.Map(t, result).HasKeyValue("description", "Returns the top 10 events by size.")
	gt.Map(t, result).HasKey("sql_content")
}

func TestRun_GetRunbookEntry_FromDir(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfigFile(t, dir, "table.yaml", minimalConfigYAML)

	sqlDir := filepath.Join(dir, "runbooks")
	gt.NoError(t, os.MkdirAll(sqlDir, 0750))

	sqlContent := "-- Title: Test Query\n-- Description: A simple test query.\nSELECT 1;\n"
	gt.NoError(t, os.WriteFile(filepath.Join(sqlDir, "test.sql"), []byte(sqlContent), 0600))

	ts := gt.R1(bqtool.New(
		bqtool.WithProjectID("my-project"),
		bqtool.WithConfigFiles([]string{cfgPath}),
		bqtool.WithRunbookPaths([]string{sqlDir}),
	)).NoError(t)
	gt.NotNil(t, ts)

	// Confirm tool loaded fine and has 6 specs.
	specs := gt.R1(ts.Specs(context.Background())).NoError(t)
	gt.Array(t, specs).Length(6)
}

// ---------------------------------------------------------------------------
// bigquery_query -> bigquery_result flow
// ---------------------------------------------------------------------------

// TestRun_QueryResultFlow exercises the two-phase query execution using an
// in-memory storage backend and a mock BigQuery client. The result file is
// pre-populated in storage so that bigquery_result reads it from cache,
// bypassing the BQ job-lookup code path.
func TestRun_QueryResultFlow(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfigFile(t, dir, "table.yaml", minimalConfigYAML)

	const testSQL = "SELECT id, value FROM my_dataset.my_table LIMIT 10"

	mockClient := bqtool.NewMockClient()
	mockClient.QueryResults[testSQL] = []map[string]any{
		{"id": "row-1", "value": int64(42)},
		{"id": "row-2", "value": int64(99)},
	}

	memStore := bqtool.NewMemStorageForTest()

	ts := gt.R1(bqtool.New(
		bqtool.WithProjectID("my-project"),
		bqtool.WithConfigFiles([]string{cfgPath}),
		bqtool.WithStorageBucket("test-bucket"),
		bqtool.WithClientFactoryForTest(&bqtool.MockClientFactory{Client: mockClient}),
		bqtool.WithStorageForTest(memStore),
	)).NoError(t)

	// Phase 1: submit query — writes metadata to in-memory storage.
	queryResult := gt.R1(ts.Run(context.Background(), "bigquery_query", map[string]any{
		"query": testSQL,
	})).NoError(t)
	queryID, ok := queryResult["query_id"].(string)
	gt.Bool(t, ok).True()
	gt.Bool(t, len(queryID) > 0).True()

	// Pre-populate the result file in the in-memory store so that
	// bigquery_result reads from cache rather than invoking jobFromIDLocation.
	rowData := "{\"id\":\"row-1\",\"value\":42}\n{\"id\":\"row-2\",\"value\":99}\n"
	bqtool.WriteStorageForTest(t, memStore, "test-bucket",
		bqtool.ResultPathForTest(queryID), []byte(rowData))

	// Phase 2: retrieve results from cache.
	resultResult := gt.R1(ts.Run(context.Background(), "bigquery_result", map[string]any{
		"query_id": queryID,
		"limit":    float64(10),
		"offset":   float64(0),
	})).NoError(t)
	gt.Map(t, resultResult).HasKey("rows_json")
	totalRows, ok := resultResult["total_rows"].(int)
	gt.Bool(t, ok).True()
	gt.Number(t, totalRows).Equal(2)
}

// TestRun_QueryResultPagination guards against a regression where the first
// (uncached) bigquery_result pass persisted only the requested page to storage
// instead of the full result set, breaking subsequent pages. It fetches page-by
// -page with limit=1: the first call reads from the BigQuery job and must cache
// ALL rows; later calls read that cache at increasing offsets.
func TestRun_QueryResultPagination(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfigFile(t, dir, "table.yaml", minimalConfigYAML)

	const testSQL = "SELECT id FROM my_dataset.my_table"

	mockClient := bqtool.NewMockClient()
	mockClient.QueryResults[testSQL] = []map[string]any{
		{"id": "row-1"},
		{"id": "row-2"},
		{"id": "row-3"},
	}

	memStore := bqtool.NewMemStorageForTest()

	ts := gt.R1(bqtool.New(
		bqtool.WithProjectID("my-project"),
		bqtool.WithConfigFiles([]string{cfgPath}),
		bqtool.WithStorageBucket("test-bucket"),
		bqtool.WithClientFactoryForTest(&bqtool.MockClientFactory{Client: mockClient}),
		bqtool.WithStorageForTest(memStore),
	)).NoError(t)

	queryResult := gt.R1(ts.Run(context.Background(), "bigquery_query", map[string]any{
		"query": testSQL,
	})).NoError(t)
	queryID := gt.Cast[string](t, queryResult["query_id"])

	page := func(offset int) map[string]any {
		return gt.R1(ts.Run(context.Background(), "bigquery_result", map[string]any{
			"query_id": queryID,
			"limit":    float64(1),
			"offset":   float64(offset),
		})).NoError(t)
	}

	// First page: served from the BigQuery job; must cache all three rows.
	p0 := page(0)
	gt.Number(t, gt.Cast[int](t, p0["total_rows"])).Equal(3)
	gt.Bool(t, gt.Cast[bool](t, p0["has_more"])).True()
	gt.String(t, gt.Cast[string](t, p0["rows_json"])).Contains("row-1")

	// Second page: served from the cache written by the first call. Before the
	// fix this was empty because only the first page had been persisted.
	p1 := page(1)
	gt.Number(t, gt.Cast[int](t, p1["total_rows"])).Equal(3)
	gt.String(t, gt.Cast[string](t, p1["rows_json"])).Contains("row-2")

	// Third (last) page.
	p2 := page(2)
	gt.Number(t, gt.Cast[int](t, p2["total_rows"])).Equal(3)
	gt.String(t, gt.Cast[string](t, p2["rows_json"])).Contains("row-3")
	gt.Bool(t, gt.Cast[bool](t, p2["has_more"])).False()
}

// ---------------------------------------------------------------------------
// Invalid tool name
// ---------------------------------------------------------------------------

func TestRun_InvalidToolName(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfigFile(t, dir, "table.yaml", minimalConfigYAML)

	ts := gt.R1(bqtool.New(
		bqtool.WithProjectID("my-project"),
		bqtool.WithConfigFiles([]string{cfgPath}),
		bqtool.WithClientFactoryForTest(&bqtool.MockClientFactory{Client: bqtool.NewMockClient()}),
	)).NoError(t)

	_, err := ts.Run(context.Background(), "nonexistent_tool", nil)
	gt.Error(t, err)
}

// ---------------------------------------------------------------------------
// Live test (conditional on TEST_BIGQUERY_PROJECT_ID being set)
// ---------------------------------------------------------------------------

// TestLive exercises all six BigQuery tool functions against a real GCP project.
//
// Required env var:
//
//	TEST_BIGQUERY_PROJECT_ID  — skips the entire test when absent
//
// Optional env vars:
//
//	TEST_BIGQUERY_CREDENTIALS      — path to a service-account credentials JSON
//	TEST_BIGQUERY_STORAGE_BUCKET   — GCS bucket for query/result flow
//	TEST_BIGQUERY_CONFIG           — path to a YAML config file (enables list_dataset / table_summary)
//	TEST_BIGQUERY_DATASET          — dataset ID (enables schema + table_summary subtests)
//	TEST_BIGQUERY_TABLE            — table  ID  (enables schema + table_summary subtests)
//	TEST_BIGQUERY_QUERY            — SQL string  (enables query + result subtests)
func TestLive(t *testing.T) {
	projectID, ok := os.LookupEnv("TEST_BIGQUERY_PROJECT_ID")
	if !ok {
		t.Skip("TEST_BIGQUERY_PROJECT_ID not set")
	}

	ctx := context.Background()

	// --- build base ToolSet options ---
	var opts []bqtool.Option
	opts = append(opts, bqtool.WithProjectID(projectID))

	if creds, ok := os.LookupEnv("TEST_BIGQUERY_CREDENTIALS"); ok {
		opts = append(opts, bqtool.WithCredentials(creds))
	}

	bucket, hasBucket := os.LookupEnv("TEST_BIGQUERY_STORAGE_BUCKET")
	if hasBucket {
		opts = append(opts, bqtool.WithStorageBucket(bucket))
	}

	cfgPath, hasCfg := os.LookupEnv("TEST_BIGQUERY_CONFIG")
	if hasCfg {
		opts = append(opts, bqtool.WithConfigFiles([]string{cfgPath}))
	}

	ts := gt.R1(bqtool.New(opts...)).NoError(t)

	// Verify connectivity.
	gt.NoError(t, ts.Ping(ctx))

	// -----------------------------------------------------------------------
	// 1. bigquery_list_dataset
	// -----------------------------------------------------------------------
	t.Run("bigquery_list_dataset", func(t *testing.T) {
		if !hasCfg {
			t.Skip("TEST_BIGQUERY_CONFIG not set — skipping list_dataset")
		}
		result := gt.R1(ts.Run(ctx, "bigquery_list_dataset", nil)).NoError(t)
		gt.Map(t, result).HasKey("config")
	})

	// -----------------------------------------------------------------------
	// 2. bigquery_schema
	// -----------------------------------------------------------------------
	t.Run("bigquery_schema", func(t *testing.T) {
		datasetID, dsOK := os.LookupEnv("TEST_BIGQUERY_DATASET")
		tableID, tblOK := os.LookupEnv("TEST_BIGQUERY_TABLE")
		if !dsOK || !tblOK {
			t.Skip("TEST_BIGQUERY_DATASET or TEST_BIGQUERY_TABLE not set — skipping schema")
		}
		result := gt.R1(ts.Run(ctx, "bigquery_schema", map[string]any{
			"project_id": projectID,
			"dataset_id": datasetID,
			"table_id":   tableID,
		})).NoError(t)
		gt.Map(t, result).HasKey("schema")
	})

	// -----------------------------------------------------------------------
	// 3. bigquery_table_summary
	// -----------------------------------------------------------------------
	t.Run("bigquery_table_summary", func(t *testing.T) {
		datasetID, dsOK := os.LookupEnv("TEST_BIGQUERY_DATASET")
		tableID, tblOK := os.LookupEnv("TEST_BIGQUERY_TABLE")
		if !dsOK || !tblOK {
			t.Skip("TEST_BIGQUERY_DATASET or TEST_BIGQUERY_TABLE not set — skipping table_summary")
		}
		if !hasCfg {
			t.Skip("TEST_BIGQUERY_CONFIG not set — skipping table_summary (needs loaded config)")
		}
		result := gt.R1(ts.Run(ctx, "bigquery_table_summary", map[string]any{
			"dataset_id": datasetID,
			"table_id":   tableID,
		})).NoError(t)
		gt.Map(t, result).HasKey("tables")
		gt.Map(t, result).HasKey("total")
	})

	// -----------------------------------------------------------------------
	// 4 + 5. bigquery_query → bigquery_result  (sequential; query id flows through)
	// -----------------------------------------------------------------------
	t.Run("bigquery_query_and_result", func(t *testing.T) {
		sqlQuery, hasQuery := os.LookupEnv("TEST_BIGQUERY_QUERY")
		if !hasQuery {
			t.Skip("TEST_BIGQUERY_QUERY not set — skipping query/result flow")
		}
		if !hasBucket {
			t.Skip("TEST_BIGQUERY_STORAGE_BUCKET not set — skipping query/result flow")
		}
		if !hasCfg {
			t.Skip("TEST_BIGQUERY_CONFIG not set — skipping query/result flow (needs loaded config)")
		}

		// Phase 1 — bigquery_query: submits the job and writes metadata to GCS.
		queryResult := gt.R1(ts.Run(ctx, "bigquery_query", map[string]any{
			"query": sqlQuery,
		})).NoError(t)
		gt.Map(t, queryResult).HasKey("query_id")

		queryID, ok := queryResult["query_id"].(string)
		gt.Bool(t, ok).True()
		gt.Bool(t, len(queryID) > 0).True()

		// Phase 2 — bigquery_result: polls for completion and returns rows.
		resultResult := gt.R1(ts.Run(ctx, "bigquery_result", map[string]any{
			"query_id": queryID,
			"limit":    float64(10),
			"offset":   float64(0),
		})).NoError(t)
		gt.Map(t, resultResult).HasKey("rows_json")
		gt.Map(t, resultResult).HasKey("total_rows")
	})

	// -----------------------------------------------------------------------
	// 6. get_runbook_entry
	// -----------------------------------------------------------------------
	// This subtest is purely local: it creates a temp runbook file, builds a
	// separate ToolSet configured with that file, and verifies the entry can be
	// retrieved. It does NOT require live BigQuery connectivity.
	t.Run("get_runbook_entry", func(t *testing.T) {
		sqlContent := "-- Title: Live Test Query\n-- Description: A test runbook for live integration testing.\nSELECT 1 AS live_check;\n"

		runbookDir := t.TempDir()
		sqlPath := filepath.Join(runbookDir, "live_test.sql")
		gt.NoError(t, os.WriteFile(sqlPath, []byte(sqlContent), 0600))

		// Build a separate ToolSet that knows about the runbook directory.
		rbOpts := append([]bqtool.Option(nil), bqtool.WithProjectID(projectID))
		if creds, ok := os.LookupEnv("TEST_BIGQUERY_CREDENTIALS"); ok {
			rbOpts = append(rbOpts, bqtool.WithCredentials(creds))
		}
		rbOpts = append(rbOpts, bqtool.WithRunbookPaths([]string{runbookDir}))

		rbTS := gt.R1(bqtool.New(rbOpts...)).NoError(t)

		// Retrieve the runbook ID assigned at load time (a random UUID).
		ids := bqtool.RunbookIDsForTest(rbTS)
		gt.Array(t, ids).Length(1)

		result := gt.R1(rbTS.Run(ctx, "get_runbook_entry", map[string]any{
			"runbook_id": ids[0],
		})).NoError(t)
		gt.Map(t, result).HasKeyValue("title", "Live Test Query")
		gt.Map(t, result).HasKeyValue("description", "A test runbook for live integration testing.")
		gt.Map(t, result).HasKey("sql_content")
		gt.Map(t, result).HasKey("id")
	})
}
