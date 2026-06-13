package bigquery

import (
	"fmt"

	"cloud.google.com/go/bigquery"
)

// convertBigQueryValue converts a BigQuery value to a JSON-safe Go type.
// This is necessary because some BigQuery value types (e.g. civil.Date,
// civil.Time) are not natively serialisable to JSON by the standard library.
func convertBigQueryValue(value bigquery.Value) any {
	if value == nil {
		return nil
	}

	switch v := value.(type) {
	case string, int, int64, float64, bool:
		return v
	case []bigquery.Value:
		result := make([]any, len(v))
		for i, item := range v {
			result[i] = convertBigQueryValue(item)
		}
		return result
	case map[string]bigquery.Value:
		result := make(map[string]any, len(v))
		for key, val := range v {
			result[key] = convertBigQueryValue(val)
		}
		return result
	default:
		// Fall back to a string representation for unknown types (e.g.
		// civil.Date, civil.Time, big.Rat).
		return fmt.Sprintf("%v", v)
	}
}
