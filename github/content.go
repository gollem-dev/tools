package github

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	ghlib "github.com/google/go-github/v74/github"
	"github.com/m-mizutani/goerr/v2"
)

// getContentInput is the typed argument struct for github_get_content.
type getContentInput struct {
	Owner string `json:"owner" description:"Repository owner (organization or username)" required:"true" pattern:"^[a-zA-Z0-9][a-zA-Z0-9-]*$" minLength:"1" maxLength:"39"`
	Repo  string `json:"repo" description:"Repository name" required:"true" pattern:"^[a-zA-Z0-9_.-]+$" minLength:"1" maxLength:"100"`
	Path  string `json:"path" description:"File path in the repository (e.g., 'src/main.go', 'README.md')" required:"true" minLength:"1"`
	Ref   string `json:"ref" description:"Git reference: branch name (e.g., 'main'), tag (e.g., 'v1.0.0'), or commit SHA. Defaults to the default branch if not specified." pattern:"^[a-zA-Z0-9/_.-]+$"`
}

func (t *ToolSet) runGetContent(ctx context.Context, in getContentInput) (map[string]any, error) {
	opts := &ghlib.RepositoryContentGetOptions{}
	if in.Ref != "" {
		opts.Ref = in.Ref
	}

	fileContent, _, _, err := t.client.GetContents(ctx, in.Owner, in.Repo, in.Path, opts)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get content",
			goerr.V("owner", in.Owner),
			goerr.V("repo", in.Repo),
			goerr.V("path", in.Path))
	}

	if fileContent == nil {
		return nil, goerr.New("no content found",
			goerr.V("owner", in.Owner),
			goerr.V("repo", in.Repo),
			goerr.V("path", in.Path))
	}

	var content string
	if fileContent.Content != nil {
		// The GitHub API base64-encodes with line breaks; strip them before decoding.
		raw := strings.ReplaceAll(*fileContent.Content, "\n", "")
		decoded, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to decode file content")
		}
		content = string(decoded)
	}

	result := ContentResult{
		Repository: fmt.Sprintf("%s/%s", in.Owner, in.Repo),
		Path:       in.Path,
		Content:    content,
	}
	if fileContent.SHA != nil {
		result.SHA = *fileContent.SHA
	}
	if fileContent.HTMLURL != nil {
		result.HTMLURL = *fileContent.HTMLURL
	}
	if fileContent.Size != nil {
		result.Size = *fileContent.Size
	}

	return map[string]any{
		"repository": result.Repository,
		"path":       result.Path,
		"content":    result.Content,
		"sha":        result.SHA,
		"html_url":   result.HTMLURL,
		"size":       result.Size,
	}, nil
}
