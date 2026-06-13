package github

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	ghlib "github.com/google/go-github/v74/github"
	"github.com/m-mizutani/goerr/v2"
)

func (t *ToolSet) runGetContent(ctx context.Context, args map[string]any) (map[string]any, error) {
	owner, ok := args["owner"].(string)
	if !ok || owner == "" {
		return nil, goerr.New("owner is required")
	}

	repo, ok := args["repo"].(string)
	if !ok || repo == "" {
		return nil, goerr.New("repo is required")
	}

	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, goerr.New("path is required")
	}

	opts := &ghlib.RepositoryContentGetOptions{}
	if ref, ok := args["ref"].(string); ok && ref != "" {
		opts.Ref = ref
	}

	fileContent, _, _, err := t.client.GetContents(ctx, owner, repo, path, opts)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get content",
			goerr.V("owner", owner),
			goerr.V("repo", repo),
			goerr.V("path", path))
	}

	if fileContent == nil {
		return nil, goerr.New("no content found",
			goerr.V("owner", owner),
			goerr.V("repo", repo),
			goerr.V("path", path))
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
		Repository: fmt.Sprintf("%s/%s", owner, repo),
		Path:       path,
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
