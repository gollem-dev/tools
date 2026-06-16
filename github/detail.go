package github

import (
	"context"

	ghlib "github.com/google/go-github/v74/github"
	"github.com/m-mizutani/goerr/v2"
)

// maxPatchBytes caps the size of a single file patch returned by
// github_get_pull_request. Larger patches are truncated and flagged so the
// agent is not overwhelmed by giant diffs.
const maxPatchBytes = 20000

// parseRepoNumber extracts and validates the owner/repo/number arguments shared
// by github_get_issue and github_get_pull_request.
func parseRepoNumber(args map[string]any) (owner, repo string, number int, err error) {
	owner, ok := args["owner"].(string)
	if !ok || owner == "" {
		return "", "", 0, goerr.New("owner is required")
	}
	repo, ok = args["repo"].(string)
	if !ok || repo == "" {
		return "", "", 0, goerr.New("repo is required")
	}
	num, ok := args["number"].(float64)
	if !ok || num < 1 {
		return "", "", 0, goerr.New("number is required and must be a positive integer")
	}
	return owner, repo, int(num), nil
}

// collectIssueComments fetches every comment page for an issue or pull request.
func (t *ToolSet) collectIssueComments(ctx context.Context, owner, repo string, number int) ([]IssueComment, error) {
	opts := &ghlib.IssueListCommentsOptions{
		ListOptions: ghlib.ListOptions{PerPage: 100},
	}
	var out []IssueComment
	for {
		comments, resp, err := t.client.ListIssueComments(ctx, owner, repo, number, opts)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to list comments",
				goerr.V("owner", owner), goerr.V("repo", repo), goerr.V("number", number))
		}
		for _, c := range comments {
			ic := IssueComment{}
			if c.Body != nil {
				ic.Body = *c.Body
			}
			if c.User != nil && c.User.Login != nil {
				ic.Author = *c.User.Login
			}
			if c.CreatedAt != nil {
				ic.CreatedAt = c.CreatedAt.Time
			}
			if c.HTMLURL != nil {
				ic.URL = *c.HTMLURL
			}
			out = append(out, ic)
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

func (t *ToolSet) runGetIssue(ctx context.Context, args map[string]any) (map[string]any, error) {
	owner, repo, number, err := parseRepoNumber(args)
	if err != nil {
		return nil, err
	}

	issue, _, err := t.client.GetIssue(ctx, owner, repo, number)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get issue",
			goerr.V("owner", owner), goerr.V("repo", repo), goerr.V("number", number))
	}
	// Issues.Get also returns pull requests; steer callers to the PR tool so the
	// issue-shaped response does not hide PR-only fields (reviews, diff, refs).
	if issue.IsPullRequest() {
		return nil, goerr.New("this number is a pull request; use github_get_pull_request instead",
			goerr.V("owner", owner), goerr.V("repo", repo), goerr.V("number", number))
	}

	comments, err := t.collectIssueComments(ctx, owner, repo, number)
	if err != nil {
		return nil, err
	}

	result := IssueDetailResult{
		Number:   number,
		Labels:   labelNames(issue.Labels),
		Comments: comments,
	}
	if issue.Title != nil {
		result.Title = *issue.Title
	}
	if issue.Body != nil {
		result.Body = *issue.Body
	}
	if issue.User != nil && issue.User.Login != nil {
		result.Author = *issue.User.Login
	}
	if issue.State != nil {
		result.State = *issue.State
	}
	if issue.HTMLURL != nil {
		result.URL = *issue.HTMLURL
	}
	if issue.CreatedAt != nil {
		result.CreatedAt = issue.CreatedAt.Time
	}
	if issue.UpdatedAt != nil {
		result.UpdatedAt = issue.UpdatedAt.Time
	}
	if issue.ClosedAt != nil {
		closed := issue.ClosedAt.Time
		result.ClosedAt = &closed
	}

	return map[string]any{
		"number":     result.Number,
		"title":      result.Title,
		"body":       result.Body,
		"author":     result.Author,
		"state":      result.State,
		"url":        result.URL,
		"labels":     result.Labels,
		"created_at": result.CreatedAt,
		"updated_at": result.UpdatedAt,
		"closed_at":  result.ClosedAt,
		"comments":   result.Comments,
	}, nil
}

func (t *ToolSet) runGetPullRequest(ctx context.Context, args map[string]any) (map[string]any, error) {
	owner, repo, number, err := parseRepoNumber(args)
	if err != nil {
		return nil, err
	}
	includeFiles, _ := args["include_files"].(bool)

	pr, _, err := t.client.GetPullRequest(ctx, owner, repo, number)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get pull request",
			goerr.V("owner", owner), goerr.V("repo", repo), goerr.V("number", number))
	}

	comments, err := t.collectIssueComments(ctx, owner, repo, number)
	if err != nil {
		return nil, err
	}
	reviews, err := t.collectReviews(ctx, owner, repo, number)
	if err != nil {
		return nil, err
	}

	result := PullRequestDetailResult{
		Number:   number,
		Labels:   labelNames(pr.Labels),
		Comments: comments,
		Reviews:  reviews,
	}
	if pr.Title != nil {
		result.Title = *pr.Title
	}
	if pr.Body != nil {
		result.Body = *pr.Body
	}
	if pr.User != nil && pr.User.Login != nil {
		result.Author = *pr.User.Login
	}
	if pr.State != nil {
		result.State = *pr.State
	}
	if pr.HTMLURL != nil {
		result.URL = *pr.HTMLURL
	}
	if pr.Merged != nil {
		result.Merged = *pr.Merged
	}
	if pr.Draft != nil {
		result.Draft = *pr.Draft
	}
	if pr.Base != nil && pr.Base.Ref != nil {
		result.BaseRef = *pr.Base.Ref
	}
	if pr.Head != nil && pr.Head.Ref != nil {
		result.HeadRef = *pr.Head.Ref
	}
	if pr.CreatedAt != nil {
		result.CreatedAt = pr.CreatedAt.Time
	}
	if pr.UpdatedAt != nil {
		result.UpdatedAt = pr.UpdatedAt.Time
	}
	if pr.ClosedAt != nil {
		closed := pr.ClosedAt.Time
		result.ClosedAt = &closed
	}

	if includeFiles {
		files, err := t.collectFiles(ctx, owner, repo, number)
		if err != nil {
			return nil, err
		}
		result.Files = files
	}

	out := map[string]any{
		"number":     result.Number,
		"title":      result.Title,
		"body":       result.Body,
		"author":     result.Author,
		"state":      result.State,
		"url":        result.URL,
		"labels":     result.Labels,
		"merged":     result.Merged,
		"draft":      result.Draft,
		"base_ref":   result.BaseRef,
		"head_ref":   result.HeadRef,
		"created_at": result.CreatedAt,
		"updated_at": result.UpdatedAt,
		"closed_at":  result.ClosedAt,
		"comments":   result.Comments,
		"reviews":    result.Reviews,
	}
	if includeFiles {
		out["files"] = result.Files
	}
	return out, nil
}

// collectReviews fetches every review page for a pull request.
func (t *ToolSet) collectReviews(ctx context.Context, owner, repo string, number int) ([]PullRequestReview, error) {
	opts := &ghlib.ListOptions{PerPage: 100}
	var out []PullRequestReview
	for {
		reviews, resp, err := t.client.ListPullRequestReviews(ctx, owner, repo, number, opts)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to list reviews",
				goerr.V("owner", owner), goerr.V("repo", repo), goerr.V("number", number))
		}
		for _, r := range reviews {
			pr := PullRequestReview{}
			if r.Body != nil {
				pr.Body = *r.Body
			}
			if r.State != nil {
				pr.State = *r.State
			}
			if r.User != nil && r.User.Login != nil {
				pr.Author = *r.User.Login
			}
			if r.SubmittedAt != nil {
				pr.CreatedAt = r.SubmittedAt.Time
			}
			out = append(out, pr)
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

// collectFiles fetches every changed-file page for a pull request, truncating
// oversized patches.
func (t *ToolSet) collectFiles(ctx context.Context, owner, repo string, number int) ([]PullRequestFile, error) {
	opts := &ghlib.ListOptions{PerPage: 100}
	var out []PullRequestFile
	for {
		files, resp, err := t.client.ListPullRequestFiles(ctx, owner, repo, number, opts)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to list pull request files",
				goerr.V("owner", owner), goerr.V("repo", repo), goerr.V("number", number))
		}
		for _, f := range files {
			pf := PullRequestFile{}
			if f.Filename != nil {
				pf.Path = *f.Filename
			}
			if f.Status != nil {
				pf.Status = *f.Status
			}
			if f.Additions != nil {
				pf.Additions = *f.Additions
			}
			if f.Deletions != nil {
				pf.Deletions = *f.Deletions
			}
			if f.Patch != nil {
				patch := *f.Patch
				if len(patch) > maxPatchBytes {
					// Truncate on a rune boundary so the patch stays valid UTF-8.
					patch = truncateUTF8(patch, maxPatchBytes)
					pf.PatchTruncated = true
				}
				pf.Patch = patch
			}
			out = append(out, pf)
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

// truncateUTF8 returns the longest prefix of s that does not exceed maxBytes,
// cut on a UTF-8 rune boundary so the result is always valid UTF-8.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// utf8RuneStart reports whether b is the first byte of a UTF-8 encoded rune.
func utf8RuneStart(b byte) bool {
	return b&0xC0 != 0x80
}

// labelNames extracts the non-nil label names from a go-github label slice.
func labelNames(labels []*ghlib.Label) []string {
	var out []string
	for _, l := range labels {
		if l.Name != nil {
			out = append(out, *l.Name)
		}
	}
	return out
}
