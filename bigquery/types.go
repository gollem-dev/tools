// Package bigquery provides a gollem.ToolSet for querying Google Cloud BigQuery
// and inspecting table schemas and runbooks.
package bigquery

import (
	"github.com/google/uuid"
)

// RunbookID is the unique identifier for a runbook entry.
type RunbookID string

// String returns the string representation of the RunbookID.
func (x RunbookID) String() string {
	return string(x)
}

// newRunbookID generates a new unique RunbookID.
func newRunbookID() RunbookID {
	return RunbookID(uuid.New().String())
}

// RunbookEntry represents a single SQL runbook loaded from a file.
type RunbookEntry struct {
	ID          RunbookID `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	SQLContent  string    `json:"sql_content"`
}

// RunbookEntries is a slice of RunbookEntry pointers.
type RunbookEntries []*RunbookEntry

// Config describes a BigQuery table's metadata for the tool. It is loaded from
// YAML configuration files at construction time.
type Config struct {
	DatasetID    string             `yaml:"dataset_id"    json:"dataset_id"`
	TableID      string             `yaml:"table_id"      json:"table_id"`
	Description  string             `yaml:"description"   json:"description"`
	Columns      []ColumnConfig     `yaml:"columns"       json:"columns"`
	Partitioning PartitioningConfig `yaml:"partitioning" json:"partitioning"`

	// RunbookPaths is an optional per-config list of SQL runbook file paths or
	// directories. These are merged with the top-level WithRunbookPaths option.
	RunbookPaths []string `yaml:"runbook_paths" json:"runbook_paths"`
}

// PartitioningConfig describes BigQuery table partitioning settings.
type PartitioningConfig struct {
	Field    string `yaml:"field"     json:"field"`
	Type     string `yaml:"type"      json:"type"`
	TimeUnit string `yaml:"time_unit" json:"time_unit"`
}

// ColumnConfig describes a single BigQuery table column.
type ColumnConfig struct {
	Name         string `yaml:"name"          json:"name"`
	Description  string `yaml:"description"   json:"description"`
	ValueExample string `yaml:"value_example" json:"value_example"`
	// Type is the BigQuery field type (STRING, INTEGER, FLOAT, BOOLEAN,
	// TIMESTAMP, DATE, TIME, DATETIME, BYTES, RECORD).
	Type string `yaml:"type"          json:"type"`
	// Fields holds nested column definitions for RECORD type columns.
	Fields []ColumnConfig `yaml:"fields"        json:"fields"`
}

// queryMetadata is stored in GCS alongside a submitted BigQuery job so that a
// subsequent bigquery_result call can locate and poll the job.
type queryMetadata struct {
	JobID    string `json:"job_id"`
	Location string `json:"location"`
}
