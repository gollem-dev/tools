package notion

import "encoding/json"

// SafeClose exposes the package-internal safeClose helper for unit testing.
var SafeClose = safeClose

// FlattenPropertiesJSON decodes a raw Notion "properties" object and flattens it
// via the package-internal flattenProperties, so external tests can exercise the
// flattening logic without constructing the unexported propertyValue type.
func FlattenPropertiesJSON(raw string) (map[string]any, error) {
	var props map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &props); err != nil {
		return nil, err
	}
	return flattenProperties(props), nil
}
