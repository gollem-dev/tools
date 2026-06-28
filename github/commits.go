package github

import (
	"context"
	"fmt"

	ghlib "github.com/google/go-github/v74/github"
	"github.com/m-mizutani/goerr/v2"
)

// listCommitsInput is the typed argument struct for github_list_commits.
type listCommitsInput struct {
	Owner   string `json:"owner" description:"Repository owner (organization or username)" required:"true" pattern:"^[a-zA-Z0-9][a-zA-Z0-9-]*$" minLength:"1" maxLength:"39"`
	Repo    string `json:"repo" description:"Repository name" required:"true" pattern:"^[a-zA-Z0-9_.-]+$" minLength:"1" maxLength:"100"`
	SHA     string `json:"sha" description:"SHA or branch to start listing commits from. Defaults to the default branch."`
	Path    string `json:"path" description:"Only commits containing this file path will be returned (e.g., 'src/main.go')"`
	Author  string `json:"author" description:"GitHub login or email address to filter commits by author"`
	PerPage int    `json:"per_page" description:"Number of commits per page (default: 30, max: 100)"`
	Page    int    `json:"page" description:"Page number for pagination (default: 1)"`
}

func (t *ToolSet) runListCommits(ctx context.Context, in listCommitsInput) (map[string]any, error) {
	opts := &ghlib.CommitsListOptions{
		ListOptions: ghlib.ListOptions{PerPage: 30},
	}

	if in.SHA != "" {
		opts.SHA = in.SHA
	}
	if in.Path != "" {
		opts.Path = in.Path
	}
	if in.Author != "" {
		opts.Author = in.Author
	}
	if in.PerPage > 0 {
		opts.PerPage = min(in.PerPage, 100)
	}
	if in.Page > 0 {
		opts.Page = in.Page
	}

	commits, _, err := t.client.ListCommits(ctx, in.Owner, in.Repo, opts)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list commits",
			goerr.V("owner", in.Owner),
			goerr.V("repo", in.Repo))
	}

	results := make([]CommitResult, 0, len(commits))
	for _, commit := range commits {
		cr := CommitResult{}

		if commit.SHA != nil {
			cr.SHA = *commit.SHA
		}
		if commit.HTMLURL != nil {
			cr.HTMLURL = *commit.HTMLURL
		}
		if commit.Commit != nil {
			if commit.Commit.Message != nil {
				cr.Message = *commit.Commit.Message
			}
			if commit.Commit.Author != nil {
				if commit.Commit.Author.Name != nil {
					cr.Author = *commit.Commit.Author.Name
				}
				if commit.Commit.Author.Date != nil {
					cr.Date = commit.Commit.Author.Date.Time
				}
			}
		}
		// Prefer the GitHub login name over the raw commit author name.
		if commit.Author != nil && commit.Author.Login != nil {
			cr.Author = *commit.Author.Login
		}

		results = append(results, cr)
	}

	return map[string]any{
		"repository": fmt.Sprintf("%s/%s", in.Owner, in.Repo),
		"commits":    results,
		"count":      len(results),
	}, nil
}
