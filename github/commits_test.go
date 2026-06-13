package github_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	ghlib "github.com/google/go-github/v74/github"
	"github.com/m-mizutani/gt"
)

func TestRunListCommits(t *testing.T) {
	sha := "deadbeef"
	htmlURL := "https://github.com/octocat/Hello-World/commit/deadbeef"
	message := "Initial commit"
	authorName := "octocat"
	date := ghlib.Timestamp{Time: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}

	var capturedOwner, capturedRepo string
	fake := &fakeClient{
		listCommitsFn: func(_ context.Context, owner, repo string, _ *ghlib.CommitsListOptions) ([]*ghlib.RepositoryCommit, *ghlib.Response, error) {
			capturedOwner = owner
			capturedRepo = repo
			return []*ghlib.RepositoryCommit{
				{
					SHA:     &sha,
					HTMLURL: &htmlURL,
					Commit: &ghlib.Commit{
						Message: &message,
						Author: &ghlib.CommitAuthor{
							Name: &authorName,
							Date: &date,
						},
					},
				},
			}, nil, nil
		},
	}

	ts := newFakeToolSet(t, fake)
	result := gt.R1(ts.Run(context.Background(), "github_list_commits", map[string]any{
		"owner": "octocat",
		"repo":  "Hello-World",
	})).NoError(t)

	gt.String(t, capturedOwner).Equal("octocat")
	gt.String(t, capturedRepo).Equal("Hello-World")
	gt.Map(t, result).HasKeyValue("repository", "octocat/Hello-World")
	gt.Map(t, result).HasKey("commits")
	gt.V(t, result["count"]).Equal(1)
}

func TestRunListCommitsMissingOwner(t *testing.T) {
	fake := &fakeClient{}
	ts := newFakeToolSet(t, fake)

	_, err := ts.Run(context.Background(), "github_list_commits", map[string]any{
		"repo": "Hello-World",
	})
	gt.Error(t, err).Contains("owner is required")
}

func TestRunListCommitsMissingRepo(t *testing.T) {
	fake := &fakeClient{}
	ts := newFakeToolSet(t, fake)

	_, err := ts.Run(context.Background(), "github_list_commits", map[string]any{
		"owner": "octocat",
	})
	gt.Error(t, err).Contains("repo is required")
}

func TestRunListCommitsAPIError(t *testing.T) {
	fake := &fakeClient{
		listCommitsFn: func(_ context.Context, _, _ string, _ *ghlib.CommitsListOptions) ([]*ghlib.RepositoryCommit, *ghlib.Response, error) {
			return nil, nil, &ghlib.ErrorResponse{
				Response: &http.Response{StatusCode: http.StatusUnauthorized},
				Message:  "unauthorized",
			}
		},
	}

	ts := newFakeToolSet(t, fake)
	_, err := ts.Run(context.Background(), "github_list_commits", map[string]any{
		"owner": "octocat",
		"repo":  "Hello-World",
	})
	gt.Error(t, err).Contains("failed to list commits")
}

func TestRunListCommitsPerPageCap(t *testing.T) {
	var capturedOpts *ghlib.CommitsListOptions
	fake := &fakeClient{
		listCommitsFn: func(_ context.Context, _, _ string, opts *ghlib.CommitsListOptions) ([]*ghlib.RepositoryCommit, *ghlib.Response, error) {
			capturedOpts = opts
			return nil, nil, nil
		},
	}

	ts := newFakeToolSet(t, fake)
	gt.R1(ts.Run(context.Background(), "github_list_commits", map[string]any{
		"owner":    "octocat",
		"repo":     "Hello-World",
		"per_page": float64(200), // should be capped at 100
	})).NoError(t)

	gt.V(t, capturedOpts.PerPage).Equal(100)
}
