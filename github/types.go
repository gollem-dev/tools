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

// BlameRange represents a contiguous set of lines attributed to a single commit.
type BlameRange struct {
	StartLine     int       `json:"start_line"`
	EndLine       int       `json:"end_line"`
	CommitSHA     string    `json:"commit_sha"`
	CommitMessage string    `json:"commit_message"`
	Author        string    `json:"author"`
	Date          time.Time `json:"date"`
}
