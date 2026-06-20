package jira_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/gollem-dev/tools/jira"
	"github.com/m-mizutani/gt"
)

func TestCombineJQL(t *testing.T) {
	testCases := map[string]struct {
		project string
		jql     string
		want    string
	}{
		"empty jql": {
			project: "PROJ",
			jql:     "",
			want:    `project = "PROJ"`,
		},
		"simple filter": {
			project: "PROJ",
			jql:     "status = Done",
			want:    `project = "PROJ" AND (status = Done)`,
		},
		"filter with order by": {
			project: "PROJ",
			jql:     "status = Done ORDER BY updated DESC",
			want:    `project = "PROJ" AND (status = Done) ORDER BY updated DESC`,
		},
		"order by only": {
			project: "PROJ",
			jql:     "ORDER BY created ASC",
			want:    `project = "PROJ" ORDER BY created ASC`,
		},
		"lowercase order by": {
			project: "PROJ",
			jql:     "status = Done order by updated",
			want:    `project = "PROJ" AND (status = Done) order by updated`,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			gt.Equal(t, jira.CombineJQL(tc.project, tc.jql), tc.want)
		})
	}
}

func TestSearchIssues(t *testing.T) {
	t.Run("forwards jql/fields and formats results", func(t *testing.T) {
		ts, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			gt.Equal(t, r.URL.Path, "/rest/api/3/search/jql")
			q := r.URL.Query()
			gt.Equal(t, q.Get("jql"), `project = "PROJ" AND (status = Done)`)
			gt.Equal(t, q.Get("maxResults"), "25")
			gt.Equal(t, q.Get("fields"), "summary,status,issuetype,priority,assignee,updated")
			_, _ = w.Write([]byte(`{
				"nextPageToken": "tok2",
				"isLast": false,
				"issues": [
					{"key":"PROJ-1","fields":{
						"summary":"Fix bug","updated":"2026-01-02T03:04:05.000+0000",
						"status":{"name":"Done"},"issuetype":{"name":"Bug"},
						"priority":{"name":"High"},"assignee":{"displayName":"Bob"}}}
				]
			}`))
		})

		out, err := ts.Run(context.Background(), "jira_search_issues", map[string]any{
			"project":     "PROJ",
			"jql":         "status = Done",
			"max_results": float64(25),
		})
		gt.NoError(t, err)

		gt.Equal(t, out["next_page_token"], "tok2")
		gt.Equal(t, out["is_last"], false)
		items := out["items"].([]map[string]any)
		gt.A(t, items).Length(1)
		gt.Equal(t, items[0]["key"], "PROJ-1")
		gt.Equal(t, items[0]["summary"], "Fix bug")
		gt.Equal(t, items[0]["status"], "Done")
		gt.Equal(t, items[0]["issue_type"], "Bug")
		gt.Equal(t, items[0]["priority"], "High")
		gt.Equal(t, items[0]["assignee"], "Bob")
	})

	t.Run("forwards next_page_token", func(t *testing.T) {
		ts, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			gt.Equal(t, r.URL.Query().Get("nextPageToken"), "tokX")
			_, _ = w.Write([]byte(`{"isLast":true,"issues":[]}`))
		})
		out, err := ts.Run(context.Background(), "jira_search_issues", map[string]any{
			"next_page_token": "tokX",
		})
		gt.NoError(t, err)
		gt.A(t, out["items"].([]map[string]any)).Length(0)
	})

	t.Run("propagates server error", func(t *testing.T) {
		ts, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		})
		_, err := ts.Run(context.Background(), "jira_search_issues", map[string]any{"jql": "bad"})
		gt.Error(t, err)
	})
}
