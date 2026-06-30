// Package bigquery provides a gollem.ToolSet for querying Google Cloud BigQuery,
// inspecting table schemas, and retrieving SQL runbook entries.
package bigquery

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/dustin/go-humanize"
	"github.com/gollem-dev/gollem"
	"github.com/m-mizutani/goerr/v2"
	"google.golang.org/api/impersonate"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"gopkg.in/yaml.v3"
)

const (
	defaultTimeout      = 5 * time.Minute
	defaultScanLimitStr = "10GB"
)

// ToolSet implements gollem.ToolSet for Google Cloud BigQuery.
// All fields are unexported; configure via Option.
type ToolSet struct {
	projectID                 string
	credentials               string
	impersonateServiceAccount string
	configFiles               []string
	runbookPaths              []string
	storageBucket             string
	storagePrefix             string
	timeout                   time.Duration
	scanLimitStr              string
	scanLimit                 uint64
	logger                    *slog.Logger

	// Populated during New:
	configs    []*Config
	runbooks   map[RunbookID]*RunbookEntry
	tools      []gollem.Tool
	toolByName map[string]gollem.Tool

	// Overridable for testing:
	clientFactory bigQueryClientFactory
	storage       storageBackend // nil = use real GCS (created lazily per call)
}

// listDatasetInput is the (empty) typed input for bigquery_list_dataset.
type listDatasetInput struct{}

// queryInput is the typed input for bigquery_query.
type queryInput struct {
	Query string `json:"query" description:"The SQL query to execute" required:"true"`
}

// resultInput is the typed input for bigquery_result.
//
// Limit is a *int, not an int, so an omitted value (nil -> default 100) stays
// distinguishable from an explicit 0. Collapsing them would silently turn a
// caller-supplied limit:0 into 100, changing the result set.
type resultInput struct {
	QueryID string `json:"query_id" description:"The ID of the query to get results for" required:"true"`
	Limit   *int   `json:"limit" description:"Maximum number of rows to return (default 100 when omitted)"`
	Offset  int    `json:"offset" description:"Number of rows to skip"`
}

// tableSummaryInput is the typed input for bigquery_table_summary.
type tableSummaryInput struct {
	ProjectID string `json:"project_id" description:"The project ID to filter by (optional)"`
	DatasetID string `json:"dataset_id" description:"The dataset ID to filter by (optional)"`
	TableID   string `json:"table_id" description:"The table ID to filter by (optional)"`
}

// schemaInput is the typed input for bigquery_schema.
type schemaInput struct {
	ProjectID string `json:"project_id" description:"The project ID of the table" required:"true"`
	DatasetID string `json:"dataset_id" description:"The dataset ID of the table" required:"true"`
	TableID   string `json:"table_id" description:"The table ID to get schema for" required:"true"`
}

// runbookEntryInput is the typed input for get_runbook_entry.
type runbookEntryInput struct {
	RunbookID string `json:"runbook_id" description:"The ID of the runbook entry to retrieve" required:"true"`
}

// Startup assertions: a malformed input/output type (a broken struct tag, a
// non-object kind) is a programming error that should surface at init rather
// than on the first LLM call. See gollem docs "Validating Tool Types".
var (
	_ = gollem.MustToolSchema[listDatasetInput, map[string]any]()
	_ = gollem.MustToolSchema[queryInput, map[string]any]()
	_ = gollem.MustToolSchema[resultInput, map[string]any]()
	_ = gollem.MustToolSchema[tableSummaryInput, map[string]any]()
	_ = gollem.MustToolSchema[schemaInput, map[string]any]()
	_ = gollem.MustToolSchema[runbookEntryInput, map[string]any]()
)

var _ gollem.ToolSet = (*ToolSet)(nil)

// Option configures a ToolSet.
type Option func(*ToolSet)

// WithCredentials sets the path to a Google Cloud service account credentials
// JSON file. When empty, Application Default Credentials are used.
func WithCredentials(path string) Option {
	return func(t *ToolSet) { t.credentials = path }
}

// WithImpersonateServiceAccount sets a service account email for impersonation.
// When set, the tool obtains short-lived credentials for that account.
func WithImpersonateServiceAccount(sa string) Option {
	return func(t *ToolSet) { t.impersonateServiceAccount = sa }
}

// WithConfigFiles sets YAML configuration file or directory paths. Each entry
// may be a file (*.yaml / *.yml) or a directory that is walked recursively.
// At least one valid config file is required for most tool operations.
func WithConfigFiles(paths []string) Option {
	return func(t *ToolSet) { t.configFiles = paths }
}

// WithRunbookPaths sets file or directory paths from which SQL runbook entries
// are loaded (*.sql files). Runbooks are loaded once during New.
func WithRunbookPaths(paths []string) Option {
	return func(t *ToolSet) { t.runbookPaths = paths }
}

// WithStorageBucket sets the GCS bucket name used for storing intermediate
// query results. Required for bigquery_query / bigquery_result.
func WithStorageBucket(bucket string) Option {
	return func(t *ToolSet) { t.storageBucket = bucket }
}

// WithStoragePrefix sets an optional path prefix for GCS objects. For example,
// "tools/bq/" results in objects like "tools/bq/bigquery/<queryID>/data.json".
func WithStoragePrefix(prefix string) Option {
	return func(t *ToolSet) { t.storagePrefix = prefix }
}

// WithTimeout sets the maximum time to wait for a BigQuery job to complete
// when calling bigquery_result. Defaults to 5 minutes.
func WithTimeout(d time.Duration) Option {
	return func(t *ToolSet) {
		if d > 0 {
			t.timeout = d
		}
	}
}

// WithScanLimit sets the maximum bytes a query may scan (parsed via
// go-humanize, e.g. "10GB"). Defaults to "10GB".
func WithScanLimit(s string) Option {
	return func(t *ToolSet) {
		if s != "" {
			t.scanLimitStr = s
		}
	}
}

// WithLogger sets the structured logger. A nil logger keeps slog.Default().
func WithLogger(logger *slog.Logger) Option {
	return func(t *ToolSet) {
		if logger != nil {
			t.logger = logger
		}
	}
}

// New constructs the ToolSet with the required projectID, applies opts,
// loads config files from disk, and loads runbook SQL files. No network
// I/O is performed; use Ping to verify connectivity.
func New(projectID string, opts ...Option) (*ToolSet, error) {
	if projectID == "" {
		return nil, goerr.New("BigQuery project ID is required")
	}

	t := &ToolSet{
		projectID:     projectID,
		timeout:       defaultTimeout,
		scanLimitStr:  defaultScanLimitStr,
		logger:        slog.Default(),
		runbooks:      make(map[RunbookID]*RunbookEntry),
		clientFactory: &defaultBigQueryClientFactory{},
	}
	for _, opt := range opts {
		opt(t)
	}

	// Parse scan limit.
	limit, err := humanize.ParseBytes(t.scanLimitStr)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to parse scan limit",
			goerr.V("scan_limit", t.scanLimitStr))
	}
	t.scanLimit = limit

	// Load table configs from files/dirs.
	if len(t.configFiles) > 0 {
		configs, err := loadConfigs(t.configFiles)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to load BigQuery config files")
		}
		t.configs = configs
	}

	// Collect runbook paths from top-level option plus any paths embedded in
	// config files.
	allRunbookPaths := append([]string(nil), t.runbookPaths...)
	for _, cfg := range t.configs {
		allRunbookPaths = append(allRunbookPaths, cfg.RunbookPaths...)
	}
	if len(allRunbookPaths) > 0 {
		if err := t.loadRunbooks(allRunbookPaths); err != nil {
			return nil, goerr.Wrap(err, "failed to load runbooks")
		}
	}

	t.tools = t.buildTools()
	t.toolByName = indexTools(t.tools)

	return t, nil
}

// indexTools builds a name->tool lookup so Run dispatches in O(1) instead of
// scanning (and re-deriving Spec()) on every call. The map is built once at
// construction and never mutated, so it is safe for concurrent Run calls.
func indexTools(tools []gollem.Tool) map[string]gollem.Tool {
	byName := make(map[string]gollem.Tool, len(tools))
	for _, tool := range tools {
		byName[tool.Spec().Name] = tool
	}
	return byName
}

// Ping verifies connectivity by creating a BigQuery client. If the underlying
// client is a real *bigquery.Client, it calls Datasets to validate credentials
// and network access (iterating even a single entry proves the connection
// works; iterator.Done — no datasets — is also a success). If WithStorageBucket
// is set it also creates a GCS client.
func (t *ToolSet) Ping(ctx context.Context) error {
	opts, err := t.clientOptions(ctx)
	if err != nil {
		return goerr.Wrap(err, "failed to build client options for ping")
	}

	client, err := t.clientFactory.NewClient(ctx, t.projectID, opts...)
	if err != nil {
		return goerr.Wrap(err, "BigQuery ping: failed to create client")
	}
	defer safeClose(t.logger, client)

	// For the real default client, call Datasets to exercise credentials.
	if dc, ok := client.(*defaultBigQueryClient); ok {
		if pingErr := pingDatasets(ctx, dc); pingErr != nil {
			return goerr.Wrap(pingErr, "BigQuery ping: dataset list failed")
		}
	}

	if t.storageBucket != "" {
		storageClient, err := t.newStorageClient(ctx)
		if err != nil {
			return goerr.Wrap(err, "BigQuery ping: failed to create GCS client")
		}
		safeClose(t.logger, storageClient)
	}

	return nil
}

// Specs returns the tool specifications derived from the typed tools.
func (t *ToolSet) Specs(_ context.Context) ([]gollem.ToolSpec, error) {
	specs := make([]gollem.ToolSpec, len(t.tools))
	for i, tool := range t.tools {
		specs[i] = tool.Spec()
	}
	return specs, nil
}

// Run dispatches to the matching typed tool by name.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	tool, ok := t.toolByName[name]
	if !ok {
		return nil, goerr.New("invalid function name", goerr.V("name", name))
	}
	return tool.Run(ctx, args)
}

// buildTools constructs the typed BigQuery tools. Each tool has a distinct input
// struct so its schema is the single source of truth for both spec generation and
// argument decoding. MustNewTool is used because the In/Out types are static: a
// build failure is a programming error (already guarded by the package-level
// MustToolSchema), not a runtime condition New should report.
func (t *ToolSet) buildTools() []gollem.Tool {
	listDataset := gollem.MustNewTool("bigquery_list_dataset",
		"List available BigQuery datasets, tables and partial schema that is necessary for investigation",
		func(ctx context.Context, in listDatasetInput) (map[string]any, error) {
			if len(t.configs) == 0 {
				return nil, goerr.New("no BigQuery configuration loaded; use WithConfigFiles")
			}
			return t.listDatasets()
		})

	queryTool := gollem.MustNewTool("bigquery_query",
		bigqueryQueryPrompt(t.scanLimitStr),
		func(ctx context.Context, in queryInput) (map[string]any, error) {
			if in.Query == "" {
				return nil, goerr.New("query parameter is required")
			}
			if len(t.configs) == 0 {
				return nil, goerr.New("no BigQuery configuration loaded; use WithConfigFiles")
			}
			opts, err := t.clientOptions(ctx)
			if err != nil {
				return nil, goerr.Wrap(err, "failed to build BigQuery client options")
			}
			client, err := t.clientFactory.NewClient(ctx, t.projectID, opts...)
			if err != nil {
				return nil, goerr.Wrap(err, "failed to create BigQuery client")
			}
			defer safeClose(t.logger, client)
			return t.executeQuery(ctx, client, in.Query)
		})

	resultTool := gollem.MustNewTool("bigquery_result",
		"Get the results of a previously executed query. Returns rows as JSON string in 'rows_json' field due to Vertex AI type limitations.",
		func(ctx context.Context, in resultInput) (map[string]any, error) {
			if in.QueryID == "" {
				return nil, goerr.New("query_id parameter is required")
			}
			if len(t.configs) == 0 {
				return nil, goerr.New("no BigQuery configuration loaded; use WithConfigFiles")
			}
			limit := 100
			if in.Limit != nil {
				limit = *in.Limit
			}
			opts, err := t.clientOptions(ctx)
			if err != nil {
				return nil, goerr.Wrap(err, "failed to build BigQuery client options")
			}
			client, err := t.clientFactory.NewClient(ctx, t.projectID, opts...)
			if err != nil {
				return nil, goerr.Wrap(err, "failed to create BigQuery client")
			}
			defer safeClose(t.logger, client)
			return t.getQueryResults(ctx, client, in.QueryID, limit, in.Offset)
		})

	tableSummaryTool := gollem.MustNewTool("bigquery_table_summary",
		"Get a summary of available BigQuery tables including all fields, examples, and descriptions",
		func(ctx context.Context, in tableSummaryInput) (map[string]any, error) {
			if len(t.configs) == 0 {
				return nil, goerr.New("no BigQuery configuration loaded; use WithConfigFiles")
			}
			return t.getTableSummary(in.ProjectID, in.DatasetID, in.TableID)
		})

	schemaTool := gollem.MustNewTool("bigquery_schema",
		"Get detailed schema information for a specific BigQuery table",
		func(ctx context.Context, in schemaInput) (map[string]any, error) {
			if in.ProjectID == "" {
				return nil, goerr.New("project_id parameter is required")
			}
			if in.DatasetID == "" {
				return nil, goerr.New("dataset_id parameter is required")
			}
			if in.TableID == "" {
				return nil, goerr.New("table_id parameter is required")
			}
			if in.ProjectID == t.projectID {
				opts, err := t.clientOptions(ctx)
				if err != nil {
					return nil, goerr.Wrap(err, "failed to build BigQuery client options")
				}
				client, err := t.clientFactory.NewClient(ctx, t.projectID, opts...)
				if err != nil {
					return nil, goerr.Wrap(err, "failed to create BigQuery client")
				}
				defer safeClose(t.logger, client)
				return t.getTableSchemaWithClient(ctx, client, in.DatasetID, in.TableID)
			}
			return t.getTableSchema(ctx, in.ProjectID, in.DatasetID, in.TableID)
		})

	runbookTool := gollem.MustNewTool("get_runbook_entry",
		"Get a specific runbook entry by its ID. Returns the SQL content and description of the runbook.",
		func(ctx context.Context, in runbookEntryInput) (map[string]any, error) {
			if in.RunbookID == "" {
				return nil, goerr.New("runbook_id parameter is required")
			}
			return t.getRunbookEntry(in.RunbookID)
		})

	return []gollem.Tool{listDataset, queryTool, resultTool, tableSummaryTool, schemaTool, runbookTool}
}

// clientOptions returns the google API client options derived from credentials
// and impersonation settings.
func (t *ToolSet) clientOptions(ctx context.Context) ([]option.ClientOption, error) {
	var opts []option.ClientOption
	if t.credentials != "" {
		opts = append(opts, option.WithCredentialsFile(t.credentials)) //nolint:staticcheck // path is from trusted configuration, not external input
	}
	if t.impersonateServiceAccount != "" {
		ts, err := impersonate.CredentialsTokenSource(ctx, impersonate.CredentialsConfig{
			TargetPrincipal: t.impersonateServiceAccount,
			Scopes: []string{
				"https://www.googleapis.com/auth/bigquery",
				"https://www.googleapis.com/auth/cloud-platform",
			},
		})
		if err != nil {
			return nil, goerr.Wrap(err, "failed to create impersonated credentials",
				goerr.V("service_account", t.impersonateServiceAccount))
		}
		opts = append(opts, option.WithTokenSource(ts))
	}
	return opts, nil
}

// newStorageClient creates a GCS client using the same credential settings.
func (t *ToolSet) newStorageClient(ctx context.Context) (*storage.Client, error) {
	var opts []option.ClientOption
	if t.credentials != "" {
		opts = append(opts, option.WithCredentialsFile(t.credentials)) //nolint:staticcheck
	}
	if t.impersonateServiceAccount != "" {
		ts, err := impersonate.CredentialsTokenSource(ctx, impersonate.CredentialsConfig{
			TargetPrincipal: t.impersonateServiceAccount,
			Scopes: []string{
				"https://www.googleapis.com/auth/devstorage.read_write",
				"https://www.googleapis.com/auth/cloud-platform",
			},
		})
		if err != nil {
			return nil, goerr.Wrap(err, "failed to create impersonated GCS credentials")
		}
		opts = append(opts, option.WithTokenSource(ts))
	}
	return storage.NewClient(ctx, opts...)
}

// loadConfigs reads Config structs from the given file/directory paths.
func loadConfigs(paths []string) ([]*Config, error) {
	var configs []*Config
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to stat config path", goerr.V("path", p))
		}
		if info.IsDir() {
			dirConfigs, err := loadConfigsFromDir(p)
			if err != nil {
				return nil, err
			}
			configs = append(configs, dirConfigs...)
		} else {
			cfg, err := loadConfigFromFile(p)
			if err != nil {
				return nil, err
			}
			configs = append(configs, cfg)
		}
	}
	return configs, nil
}

func loadConfigFromFile(filePath string) (*Config, error) {
	data, err := os.ReadFile(filepath.Clean(filePath))
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read config file", goerr.V("path", filePath))
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, goerr.Wrap(err, "failed to parse config file", goerr.V("path", filePath))
	}
	return &cfg, nil
}

func loadConfigsFromDir(dirPath string) ([]*Config, error) {
	var configs []*Config
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return goerr.Wrap(err, "failed to walk directory", goerr.V("path", path))
		}
		if info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		cfg, err := loadConfigFromFile(path)
		if err != nil {
			return err
		}
		configs = append(configs, cfg)
		return nil
	})
	return configs, err
}

// pingDatasets calls the BigQuery Datasets API on dc to validate credentials
// and connectivity. iterator.Done (no datasets in the project) is treated as
// success because the API call itself succeeded.
func pingDatasets(ctx context.Context, dc *defaultBigQueryClient) error {
	it := dc.client.Datasets(ctx)
	_, err := it.Next()
	if err != nil && err != iterator.Done {
		return goerr.Wrap(err, "BigQuery ping: failed to list datasets")
	}
	return nil
}

// loadRunbooks reads SQL runbook entries from paths and populates t.runbooks.
func (t *ToolSet) loadRunbooks(paths []string) error {
	loader := newRunbookLoader(paths)
	entries, err := loader.loadRunbooks()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		t.runbooks[entry.ID] = entry
	}
	return nil
}
