package bigquery

import (
	"bytes"
	_ "embed"
	"text/template"
)

//go:embed prompt/bigquery_query.md
var bigqueryQueryPromptTemplate string

// bigqueryQueryPrompt renders the bigquery_query tool description, substituting
// the configured scan limit so the LLM knows the constraint.
func bigqueryQueryPrompt(scanLimit string) string {
	tmpl := template.Must(template.New("bigquery_query").Parse(bigqueryQueryPromptTemplate))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{"limit": scanLimit}); err != nil {
		return ""
	}
	return buf.String()
}
