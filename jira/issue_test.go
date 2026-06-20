package jira_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/m-mizutani/gt"
)

func TestGetIssues(t *testing.T) {
	t.Run("batches keys, converts description, reports not_found", func(t *testing.T) {
		ts, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			gt.Equal(t, r.Method, http.MethodPost)
			gt.Equal(t, r.URL.Path, "/rest/api/3/issue/bulkfetch")

			body, _ := io.ReadAll(r.Body)
			var req map[string]any
			gt.NoError(t, json.Unmarshal(body, &req))
			keys := req["issueIdsOrKeys"].([]any)
			gt.A(t, keys).Length(2)
			gt.Equal(t, keys[0], "PROJ-1")
			gt.Equal(t, keys[1], "PROJ-2")
			// comments not requested -> "comment" must be absent from fields
			for _, f := range req["fields"].([]any) {
				gt.NotEqual(t, f, "comment")
			}

			_, _ = w.Write([]byte(`{
				"issues": [
					{"key":"PROJ-1","fields":{
						"summary":"A","status":{"name":"Open"},"issuetype":{"name":"Task"},
						"priority":{"name":"Low"},"assignee":{"displayName":"Bob"},
						"reporter":{"displayName":"Carol"},"labels":["x","y"],
						"created":"2026-01-01","updated":"2026-01-02",
						"description":{"type":"doc","version":1,"content":[
							{"type":"paragraph","content":[{"type":"text","text":"Hello "},
							{"type":"text","text":"world","marks":[{"type":"strong"}]}]}
						]}}}
				],
				"notFoundIssueKeys": ["PROJ-2"]
			}`))
		})

		out, err := ts.Run(context.Background(), "jira_get_issues", map[string]any{
			"issue_keys": []any{"PROJ-1", "PROJ-2"},
		})
		gt.NoError(t, err)

		items := out["items"].([]map[string]any)
		gt.A(t, items).Length(1)
		gt.Equal(t, items[0]["key"], "PROJ-1")
		gt.Equal(t, items[0]["summary"], "A")
		gt.Equal(t, items[0]["status"], "Open")
		gt.Equal(t, items[0]["reporter"], "Carol")
		gt.Equal(t, items[0]["labels"].([]string), []string{"x", "y"})
		gt.Equal(t, items[0]["description"], "Hello **world**")
		// comments omitted when not requested
		_, hasComments := items[0]["comments"]
		gt.False(t, hasComments)

		gt.Equal(t, out["not_found"].([]string), []string{"PROJ-2"})
	})

	t.Run("includes comments when requested", func(t *testing.T) {
		ts, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			var req map[string]any
			gt.NoError(t, json.Unmarshal(body, &req))
			hasComment := false
			for _, f := range req["fields"].([]any) {
				if f == "comment" {
					hasComment = true
				}
			}
			gt.True(t, hasComment)

			_, _ = w.Write([]byte(`{
				"issues": [
					{"key":"PROJ-1","fields":{
						"summary":"A","comment":{"comments":[
							{"author":{"displayName":"Dave"},"created":"2026-02-02",
							 "body":{"type":"doc","content":[{"type":"paragraph",
							 "content":[{"type":"text","text":"a comment"}]}]}}
						]}}}
				]
			}`))
		})

		out, err := ts.Run(context.Background(), "jira_get_issues", map[string]any{
			"issue_keys":       []any{"PROJ-1"},
			"include_comments": true,
		})
		gt.NoError(t, err)
		items := out["items"].([]map[string]any)
		comments := items[0]["comments"].([]map[string]any)
		gt.A(t, comments).Length(1)
		gt.Equal(t, comments[0]["author"], "Dave")
		gt.Equal(t, comments[0]["body"], "a comment")
	})

	t.Run("rejects empty issue_keys", func(t *testing.T) {
		ts, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			t.Error("server should not be called")
		})
		_, err := ts.Run(context.Background(), "jira_get_issues", map[string]any{
			"issue_keys": []any{},
		})
		gt.Error(t, err)
	})

	t.Run("rejects non-string entries", func(t *testing.T) {
		ts, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			t.Error("server should not be called")
		})
		_, err := ts.Run(context.Background(), "jira_get_issues", map[string]any{
			"issue_keys": []any{"PROJ-1", 42},
		})
		gt.Error(t, err)
	})
}
