// Package github provides a gollem.ToolSet for GitHub code/issue search,
// file content retrieval, commit history listing, and git blame.
package github

import "time"

// CodeSearchResult represents a single code search result.
type CodeSearchResult struct {
	Repository string   `json:"repository"`
	Path       string   `json:"path"`
	HTMLURL    string   `json:"html_url"`
	Matches    []string `json:"matches"`
}

// IssueSearchResult represents a single issue or pull-request search result.
type IssueSearchResult struct {
	Repository string    `json:"repository"`
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	State      string    `json:"state"`
	HTMLURL    string    `json:"html_url"`
	User       string    `json:"user"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	IsPR       bool      `json:"is_pr"`
	Body       string    `json:"body,omitempty"`
	Labels     []string  `json:"labels,omitempty"`
}

// ContentResult represents file content retrieved from a repository.
type ContentResult struct {
	Repository string `json:"repository"`
	Path       string `json:"path"`
	Content    string `json:"content"`
	SHA        string `json:"sha"`
	HTMLURL    string `json:"html_url"`
	Size       int    `json:"size"`
}

// CommitResult represents a single commit in the list-commits response.
type CommitResult struct {
	SHA     string    `json:"sha"`
	Message string    `json:"message"`
	Author  string    `json:"author"`
	Date    time.Time `json:"date"`
	HTMLURL string    `json:"html_url"`
}

// IssueComment represents a single comment on an issue or pull request.
type IssueComment struct {
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	URL       string    `json:"url"`
}

// IssueDetailResult represents a single issue fetched with full body and comments.
type IssueDetailResult struct {
	Number    int            `json:"number"`
	Title     string         `json:"title"`
	Body      string         `json:"body"`
	Author    string         `json:"author"`
	State     string         `json:"state"`
	URL       string         `json:"url"`
	Labels    []string       `json:"labels"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	ClosedAt  *time.Time     `json:"closed_at,omitempty"`
	Comments  []IssueComment `json:"comments"`
}

// PullRequestReview represents a single review on a pull request.
type PullRequestReview struct {
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}

// PullRequestFile represents a single changed file in a pull request.
type PullRequestFile struct {
	Path           string `json:"path"`
	Status         string `json:"status"`
	Additions      int    `json:"additions"`
	Deletions      int    `json:"deletions"`
	Patch          string `json:"patch"`
	PatchTruncated bool   `json:"patch_truncated"`
}

// PullRequestDetailResult represents a single pull request fetched with body,
// comments, reviews, and optionally the file diff.
type PullRequestDetailResult struct {
	Number    int                 `json:"number"`
	Title     string              `json:"title"`
	Body      string              `json:"body"`
	Author    string              `json:"author"`
	State     string              `json:"state"`
	URL       string              `json:"url"`
	Labels    []string            `json:"labels"`
	Merged    bool                `json:"merged"`
	Draft     bool                `json:"draft"`
	BaseRef   string              `json:"base_ref"`
	HeadRef   string              `json:"head_ref"`
	CreatedAt time.Time           `json:"created_at"`
	UpdatedAt time.Time           `json:"updated_at"`
	ClosedAt  *time.Time          `json:"closed_at,omitempty"`
	Comments  []IssueComment      `json:"comments"`
	Reviews   []PullRequestReview `json:"reviews"`
	Files     []PullRequestFile   `json:"files,omitempty"`
}

// BlameRange represents a contiguous set of lines attributed to a single commit.
type BlameRange struct {
	StartLine     int       `json:"start_line"`
	EndLine       int       `json:"end_line"`
	CommitSHA     string    `json:"commit_sha"`
	CommitMessage string    `json:"commit_message"`
	Author        string    `json:"author"`
	Date          time.Time `json:"date"`
}
