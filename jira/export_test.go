package jira

import "encoding/json"

// SafeClose exposes the package-internal safeClose helper for unit testing.
var SafeClose = safeClose

// CombineJQL exposes the package-internal combineJQL helper so external tests
// can verify how the project filter is spliced into a JQL string.
var CombineJQL = combineJQL

// ADFToMarkdown converts a raw ADF JSON document to Markdown via the
// package-internal adfToMarkdown, exposed for external tests.
func ADFToMarkdown(raw string) string {
	return adfToMarkdown(json.RawMessage(raw))
}
