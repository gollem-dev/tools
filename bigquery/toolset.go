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
	configs  []*Config
	runbooks map[RunbookID]*RunbookEntry

	// Overridable for testing:
	clientFactory bigQueryClientFactory
	storage       storageBackend // nil = use real GCS (created lazily per call)
}

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

	return t, nil
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

// Specs returns the tool specifications for the BigQuery tool set.
func (t *ToolSet) Specs(_ context.Context) ([]gollem.ToolSpec, error) {
	return []gollem.ToolSpec{
		{
			Name:        "bigquery_list_dataset",
			Description: "List available BigQuery datasets, tables and partial schema that is necessary for investigation",
			Parameters:  map[string]*gollem.Parameter{},
		},
		{
			Name:        "bigquery_query",
			Description: bigqueryQueryPrompt(t.scanLimitStr),
			Parameters: map[string]*gollem.Parameter{
				"query": {
					Type:        gollem.TypeString,
					Description: "The SQL query to execute",
					Required:    true,
				},
			},
		},
		{
			Name:        "bigquery_result",
			Description: "Get the results of a previously executed query. Returns rows as JSON string in 'rows_json' field due to Vertex AI type limitations.",
			Parameters: map[string]*gollem.Parameter{
				"query_id": {
					Type:        gollem.TypeString,
					Description: "The ID of the query to get results for",
					Required:    true,
				},
				"limit": {
					Type:        gollem.TypeInteger,
					Description: "Maximum number of rows to return",
				},
				"offset": {
					Type:        gollem.TypeInteger,
					Description: "Number of rows to skip",
				},
			},
		},
		{
			Name:        "bigquery_table_summary",
			Description: "Get a summary of available BigQuery tables including all fields, examples, and descriptions",
			Parameters: map[string]*gollem.Parameter{
				"project_id": {
					Type:        gollem.TypeString,
					Description: "The project ID to filter by (optional)",
				},
				"dataset_id": {
					Type:        gollem.TypeString,
					Description: "The dataset ID to filter by (optional)",
				},
				"table_id": {
					Type:        gollem.TypeString,
					Description: "The table ID to filter by (optional)",
				},
			},
		},
		{
			Name:        "bigquery_schema",
			Description: "Get detailed schema information for a specific BigQuery table",
			Parameters: map[string]*gollem.Parameter{
				"project_id": {
					Type:        gollem.TypeString,
					Description: "The project ID of the table",
					Required:    true,
				},
				"dataset_id": {
					Type:        gollem.TypeString,
					Description: "The dataset ID of the table",
					Required:    true,
				},
				"table_id": {
					Type:        gollem.TypeString,
					Description: "The table ID to get schema for",
					Required:    true,
				},
			},
		},
		{
			Name:        "get_runbook_entry",
			Description: "Get a specific runbook entry by its ID. Returns the SQL content and description of the runbook.",
			Parameters: map[string]*gollem.Parameter{
				"runbook_id": {
					Type:        gollem.TypeString,
					Description: "The ID of the runbook entry to retrieve",
					Required:    true,
				},
			},
		},
	}, nil
}

// Run dispatches to the appropriate handler based on name.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	if args == nil {
		args = make(map[string]any)
	}

	// bigquery_table_summary is a pure in-memory operation.
	if name == "bigquery_table_summary" {
		if len(t.configs) == 0 {
			return nil, goerr.New("no BigQuery configuration loaded; use WithConfigFiles")
		}
		var projectID, datasetID, tableID string
		if v, ok := args["project_id"].(string); ok {
			projectID = v
		}
		if v, ok := args["dataset_id"].(string); ok {
			datasetID = v
		}
		if v, ok := args["table_id"].(string); ok {
			tableID = v
		}
		return t.getTableSummary(projectID, datasetID, tableID)
	}

	// get_runbook_entry is also in-memory.
	if name == "get_runbook_entry" {
		id, ok := args["runbook_id"].(string)
		if !ok || id == "" {
			return nil, goerr.New("runbook_id parameter is required")
		}
		return t.getRunbookEntry(id)
	}

	// All remaining tools need a BigQuery client.
	opts, err := t.clientOptions(ctx)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to build BigQuery client options")
	}
	client, err := t.clientFactory.NewClient(ctx, t.projectID, opts...)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create BigQuery client")
	}
	defer safeClose(t.logger, client)

	return t.runWithClient(ctx, name, args, client)
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
