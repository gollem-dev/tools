package jira_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/m-mizutani/gt"
)

func TestListProjects(t *testing.T) {
	t.Run("returns formatted projects and forwards query/pagination", func(t *testing.T) {
		ts, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			gt.Equal(t, r.URL.Path, "/rest/api/3/project/search")
			q := r.URL.Query()
			gt.Equal(t, q.Get("query"), "plat")
			gt.Equal(t, q.Get("maxResults"), "10")
			gt.Equal(t, q.Get("startAt"), "20")
			_, _ = w.Write([]byte(`{
				"isLast": true,
				"total": 1,
				"values": [
					{"id":"100","key":"PLAT","name":"Platform","projectTypeKey":"software",
					 "lead":{"displayName":"Alice"}}
				]
			}`))
		})

		out, err := ts.Run(context.Background(), "jira_list_projects", map[string]any{
			"query":       "plat",
			"max_results": float64(10),
			"start_at":    float64(20),
		})
		gt.NoError(t, err)

		gt.Equal(t, out["total"], 1)
		gt.Equal(t, out["is_last"], true)
		items := out["items"].([]map[string]any)
		gt.A(t, items).Length(1)
		gt.Equal(t, items[0]["id"], "100")
		gt.Equal(t, items[0]["key"], "PLAT")
		gt.Equal(t, items[0]["name"], "Platform")
		gt.Equal(t, items[0]["project_type"], "software")
		gt.Equal(t, items[0]["lead"], "Alice")
	})

	t.Run("defaults max_results and start_at when omitted", func(t *testing.T) {
		ts, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			gt.Equal(t, q.Get("maxResults"), "50")
			gt.Equal(t, q.Get("startAt"), "0")
			gt.Equal(t, q.Get("query"), "")
			_, _ = w.Write([]byte(`{"isLast":true,"total":0,"values":[]}`))
		})
		out, err := ts.Run(context.Background(), "jira_list_projects", map[string]any{})
		gt.NoError(t, err)
		gt.A(t, out["items"].([]map[string]any)).Length(0)
	})

	t.Run("propagates server error", func(t *testing.T) {
		ts, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		_, err := ts.Run(context.Background(), "jira_list_projects", map[string]any{})
		gt.Error(t, err)
	})
}
