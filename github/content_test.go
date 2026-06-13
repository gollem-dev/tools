package github_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"testing"

	ghlib "github.com/google/go-github/v74/github"
	"github.com/m-mizutani/gt"
)

func TestRunGetContent(t *testing.T) {
	fileBody := "package main\n\nfunc main() {}\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(fileBody))
	sha := "abc123"
	htmlURL := "https://github.com/octocat/Hello-World/blob/main/main.go"
	size := len(fileBody)

	fake := &fakeClient{
		getContentsFn: func(_ context.Context, owner, repo, path string, _ *ghlib.RepositoryContentGetOptions) (*ghlib.RepositoryContent, []*ghlib.RepositoryContent, *ghlib.Response, error) {
			gt.String(t, owner).Equal("octocat")
			gt.String(t, repo).Equal("Hello-World")
			gt.String(t, path).Equal("main.go")
			return &ghlib.RepositoryContent{
				Content: &encoded,
				SHA:     &sha,
				HTMLURL: &htmlURL,
				Size:    &size,
			}, nil, nil, nil
		},
	}

	ts := newFakeToolSet(t, fake)
	result := gt.R1(ts.Run(context.Background(), "github_get_content", map[string]any{
		"owner": "octocat",
		"repo":  "Hello-World",
		"path":  "main.go",
	})).NoError(t)

	gt.Map(t, result).HasKeyValue("repository", "octocat/Hello-World")
	gt.Map(t, result).HasKeyValue("path", "main.go")
	gt.Map(t, result).HasKeyValue("content", fileBody)
	gt.Map(t, result).HasKeyValue("sha", sha)
}

func TestRunGetContentMissingOwner(t *testing.T) {
	fake := &fakeClient{}
	ts := newFakeToolSet(t, fake)

	_, err := ts.Run(context.Background(), "github_get_content", map[string]any{
		"repo": "Hello-World",
		"path": "main.go",
	})
	gt.Error(t, err).Contains("owner is required")
}

func TestRunGetContentMissingRepo(t *testing.T) {
	fake := &fakeClient{}
	ts := newFakeToolSet(t, fake)

	_, err := ts.Run(context.Background(), "github_get_content", map[string]any{
		"owner": "octocat",
		"path":  "main.go",
	})
	gt.Error(t, err).Contains("repo is required")
}

func TestRunGetContentMissingPath(t *testing.T) {
	fake := &fakeClient{}
	ts := newFakeToolSet(t, fake)

	_, err := ts.Run(context.Background(), "github_get_content", map[string]any{
		"owner": "octocat",
		"repo":  "Hello-World",
	})
	gt.Error(t, err).Contains("path is required")
}

func TestRunGetContentAPIError(t *testing.T) {
	fake := &fakeClient{
		getContentsFn: func(_ context.Context, _, _, _ string, _ *ghlib.RepositoryContentGetOptions) (*ghlib.RepositoryContent, []*ghlib.RepositoryContent, *ghlib.Response, error) {
			return nil, nil, nil, &ghlib.ErrorResponse{
				Response: &http.Response{StatusCode: http.StatusNotFound},
				Message:  "not found",
			}
		},
	}

	ts := newFakeToolSet(t, fake)
	_, err := ts.Run(context.Background(), "github_get_content", map[string]any{
		"owner": "octocat",
		"repo":  "Hello-World",
		"path":  "nonexistent.go",
	})
	gt.Error(t, err).Contains("failed to get content")
}
