package github_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gollem-dev/tools/github"
	ghlib "github.com/google/go-github/v74/github"
	"github.com/m-mizutani/gt"
)

func TestRunGetIssue(t *testing.T) {
	created := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	updated := time.Date(2024, 1, 3, 3, 4, 5, 0, time.UTC)

	var capturedNumber int
	commentPages := [][]*ghlib.IssueComment{
		{
			{Body: ghlib.Ptr("first"), User: &ghlib.User{Login: ghlib.Ptr("alice")},
				CreatedAt: &ghlib.Timestamp{Time: created}, HTMLURL: ghlib.Ptr("https://c/1")},
		},
		{
			{Body: ghlib.Ptr("second"), User: &ghlib.User{Login: ghlib.Ptr("bob")},
				CreatedAt: &ghlib.Timestamp{Time: updated}, HTMLURL: ghlib.Ptr("https://c/2")},
		},
	}
	pageIdx := 0

	fake := &fakeClient{
		getIssueFn: func(_ context.Context, _, _ string, number int) (*ghlib.Issue, *ghlib.Response, error) {
			capturedNumber = number
			return &ghlib.Issue{
				Number:    ghlib.Ptr(number),
				Title:     ghlib.Ptr("Bug report"),
				Body:      ghlib.Ptr("something broke"),
				State:     ghlib.Ptr("open"),
				HTMLURL:   ghlib.Ptr("https://github.com/o/r/issues/42"),
				User:      &ghlib.User{Login: ghlib.Ptr("reporter")},
				Labels:    []*ghlib.Label{{Name: ghlib.Ptr("bug")}, {Name: ghlib.Ptr("p1")}},
				CreatedAt: &ghlib.Timestamp{Time: created},
				UpdatedAt: &ghlib.Timestamp{Time: updated},
			}, nil, nil
		},
		listIssueCommentsFn: func(_ context.Context, _, _ string, _ int, _ *ghlib.IssueListCommentsOptions) ([]*ghlib.IssueComment, *ghlib.Response, error) {
			page := commentPages[pageIdx]
			resp := &ghlib.Response{}
			if pageIdx < len(commentPages)-1 {
				resp.NextPage = pageIdx + 2
			}
			pageIdx++
			return page, resp, nil
		},
	}

	ts := newFakeToolSet(t, fake)
	result := gt.R1(ts.Run(context.Background(), "github_get_issue", map[string]any{
		"owner":  "o",
		"repo":   "r",
		"number": float64(42),
	})).NoError(t)

	gt.Value(t, capturedNumber).Equal(42)
	gt.Value(t, result["number"]).Equal(42)
	gt.Value(t, result["title"]).Equal("Bug report")
	gt.Value(t, result["state"]).Equal("open")
	gt.Value(t, result["author"]).Equal("reporter")
	gt.Array(t, result["labels"].([]string)).Equal([]string{"bug", "p1"})

	comments := result["comments"].([]github.IssueComment)
	gt.Array(t, comments).Length(2)
	gt.Value(t, comments[0].Author).Equal("alice")
	gt.Value(t, comments[1].Author).Equal("bob")
	// Open issue: no closed_at.
	gt.Value(t, result["closed_at"]).Nil()
}

func TestRunGetIssueClosed(t *testing.T) {
	closed := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	fake := &fakeClient{
		getIssueFn: func(_ context.Context, _, _ string, number int) (*ghlib.Issue, *ghlib.Response, error) {
			return &ghlib.Issue{
				Number:   ghlib.Ptr(number),
				Title:    ghlib.Ptr("done"),
				State:    ghlib.Ptr("closed"),
				ClosedAt: &ghlib.Timestamp{Time: closed},
			}, nil, nil
		},
		listIssueCommentsFn: func(_ context.Context, _, _ string, _ int, _ *ghlib.IssueListCommentsOptions) ([]*ghlib.IssueComment, *ghlib.Response, error) {
			return nil, &ghlib.Response{}, nil
		},
	}

	ts := newFakeToolSet(t, fake)
	result := gt.R1(ts.Run(context.Background(), "github_get_issue", map[string]any{
		"owner": "o", "repo": "r", "number": float64(1),
	})).NoError(t)

	closedAt := result["closed_at"].(*time.Time)
	gt.Value(t, closedAt.Equal(closed)).Equal(true)
}

func TestRunGetIssueRejectsPullRequest(t *testing.T) {
	fake := &fakeClient{
		getIssueFn: func(_ context.Context, _, _ string, number int) (*ghlib.Issue, *ghlib.Response, error) {
			return &ghlib.Issue{
				Number:           ghlib.Ptr(number),
				Title:            ghlib.Ptr("a PR"),
				PullRequestLinks: &ghlib.PullRequestLinks{URL: ghlib.Ptr("https://github.com/o/r/pull/7")},
			}, nil, nil
		},
	}

	ts := newFakeToolSet(t, fake)
	_, err := ts.Run(context.Background(), "github_get_issue", map[string]any{
		"owner": "o", "repo": "r", "number": float64(7),
	})
	gt.Error(t, err).Contains("github_get_pull_request")
}

func TestRunGetIssueMissingArgs(t *testing.T) {
	ts := newFakeToolSet(t, &fakeClient{})
	_, err := ts.Run(context.Background(), "github_get_issue", map[string]any{
		"owner": "o", "repo": "r",
	})
	gt.Error(t, err).Contains("number")
}

func TestRunGetPullRequest(t *testing.T) {
	created := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2024, 2, 2, 0, 0, 0, 0, time.UTC)

	fake := &fakeClient{
		getPullRequestFn: func(_ context.Context, _, _ string, number int) (*ghlib.PullRequest, *ghlib.Response, error) {
			return &ghlib.PullRequest{
				Number:    ghlib.Ptr(number),
				Title:     ghlib.Ptr("Add feature"),
				Body:      ghlib.Ptr("implements X"),
				State:     ghlib.Ptr("open"),
				HTMLURL:   ghlib.Ptr("https://github.com/o/r/pull/10"),
				User:      &ghlib.User{Login: ghlib.Ptr("dev")},
				Labels:    []*ghlib.Label{{Name: ghlib.Ptr("enhancement")}},
				Merged:    ghlib.Ptr(false),
				Draft:     ghlib.Ptr(true),
				Base:      &ghlib.PullRequestBranch{Ref: ghlib.Ptr("main")},
				Head:      &ghlib.PullRequestBranch{Ref: ghlib.Ptr("feature/x")},
				CreatedAt: &ghlib.Timestamp{Time: created},
				UpdatedAt: &ghlib.Timestamp{Time: updated},
			}, nil, nil
		},
		listIssueCommentsFn: func(_ context.Context, _, _ string, _ int, _ *ghlib.IssueListCommentsOptions) ([]*ghlib.IssueComment, *ghlib.Response, error) {
			return []*ghlib.IssueComment{
				{Body: ghlib.Ptr("nice"), User: &ghlib.User{Login: ghlib.Ptr("rev")}, CreatedAt: &ghlib.Timestamp{Time: created}},
			}, &ghlib.Response{}, nil
		},
		listReviewsFn: func(_ context.Context, _, _ string, _ int, _ *ghlib.ListOptions) ([]*ghlib.PullRequestReview, *ghlib.Response, error) {
			return []*ghlib.PullRequestReview{
				{Body: ghlib.Ptr("LGTM"), State: ghlib.Ptr("APPROVED"), User: &ghlib.User{Login: ghlib.Ptr("rev")}, SubmittedAt: &ghlib.Timestamp{Time: updated}},
			}, &ghlib.Response{}, nil
		},
	}

	ts := newFakeToolSet(t, fake)
	result := gt.R1(ts.Run(context.Background(), "github_get_pull_request", map[string]any{
		"owner": "o", "repo": "r", "number": float64(10),
	})).NoError(t)

	gt.Value(t, result["number"]).Equal(10)
	gt.Value(t, result["title"]).Equal("Add feature")
	gt.Value(t, result["draft"]).Equal(true)
	gt.Value(t, result["merged"]).Equal(false)
	gt.Value(t, result["base_ref"]).Equal("main")
	gt.Value(t, result["head_ref"]).Equal("feature/x")

	comments := result["comments"].([]github.IssueComment)
	gt.Array(t, comments).Length(1)
	reviews := result["reviews"].([]github.PullRequestReview)
	gt.Array(t, reviews).Length(1)
	gt.Value(t, reviews[0].State).Equal("APPROVED")

	// include_files defaulted to false: no files key.
	_, hasFiles := result["files"]
	gt.Value(t, hasFiles).Equal(false)
}

func TestRunGetPullRequestWithFiles(t *testing.T) {
	bigPatch := strings.Repeat("a", 25000)

	fake := &fakeClient{
		getPullRequestFn: func(_ context.Context, _, _ string, number int) (*ghlib.PullRequest, *ghlib.Response, error) {
			return &ghlib.PullRequest{Number: ghlib.Ptr(number), Title: ghlib.Ptr("pr"), State: ghlib.Ptr("open")}, nil, nil
		},
		listIssueCommentsFn: func(_ context.Context, _, _ string, _ int, _ *ghlib.IssueListCommentsOptions) ([]*ghlib.IssueComment, *ghlib.Response, error) {
			return nil, &ghlib.Response{}, nil
		},
		listReviewsFn: func(_ context.Context, _, _ string, _ int, _ *ghlib.ListOptions) ([]*ghlib.PullRequestReview, *ghlib.Response, error) {
			return nil, &ghlib.Response{}, nil
		},
		listFilesFn: func(_ context.Context, _, _ string, _ int, _ *ghlib.ListOptions) ([]*ghlib.CommitFile, *ghlib.Response, error) {
			return []*ghlib.CommitFile{
				{Filename: ghlib.Ptr("small.go"), Status: ghlib.Ptr("modified"), Additions: ghlib.Ptr(3), Deletions: ghlib.Ptr(1), Patch: ghlib.Ptr("@@ small @@")},
				{Filename: ghlib.Ptr("big.go"), Status: ghlib.Ptr("added"), Additions: ghlib.Ptr(900), Deletions: ghlib.Ptr(0), Patch: ghlib.Ptr(bigPatch)},
			}, &ghlib.Response{}, nil
		},
	}

	ts := newFakeToolSet(t, fake)
	result := gt.R1(ts.Run(context.Background(), "github_get_pull_request", map[string]any{
		"owner": "o", "repo": "r", "number": float64(10), "include_files": true,
	})).NoError(t)

	files := result["files"].([]github.PullRequestFile)
	gt.Array(t, files).Length(2)
	gt.Value(t, files[0].Path).Equal("small.go")
	gt.Value(t, files[0].PatchTruncated).Equal(false)
	gt.Value(t, files[1].Path).Equal("big.go")
	gt.Value(t, files[1].PatchTruncated).Equal(true)
	// Truncated patch must not exceed the byte cap.
	gt.Value(t, len(files[1].Patch) <= 20000).Equal(true)
}
