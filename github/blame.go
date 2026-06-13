package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gollem-dev/tools/internal/safe"
	"github.com/m-mizutani/goerr/v2"
)

// graphQLRequest is the JSON body sent to the GitHub GraphQL endpoint.
type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// graphQLResponse is the top-level JSON envelope returned by GraphQL.
type graphQLResponse struct {
	Data   graphQLBlameData `json:"data"`
	Errors []graphQLError   `json:"errors,omitempty"`
}

type graphQLError struct {
	Message string `json:"message"`
}

type graphQLBlameData struct {
	Repository graphQLRepository `json:"repository"`
}

type graphQLRepository struct {
	Object *graphQLObject `json:"object"`
}

type graphQLObject struct {
	Blame *graphQLBlame `json:"blame"`
}

type graphQLBlame struct {
	Ranges []graphQLBlameRange `json:"ranges"`
}

type graphQLBlameRange struct {
	StartingLine int              `json:"startingLine"`
	EndingLine   int              `json:"endingLine"`
	Commit       graphQLCommitRef `json:"commit"`
}

type graphQLCommitRef struct {
	OID     string              `json:"oid"`
	Message string              `json:"message"`
	Author  graphQLCommitAuthor `json:"author"`
}

type graphQLCommitAuthor struct {
	Name string    `json:"name"`
	Date time.Time `json:"date"`
}

// blameQuery resolves the given ref to a Commit and blames the file at path.
// GitHub's GraphQL `blame` field lives on Commit (not Blob) and takes the path
// as an argument. Passing "HEAD" as the ref resolves to the repository's
// default branch, regardless of whether it is main, master, or anything else.
const blameQuery = `query($owner: String!, $name: String!, $ref: String!, $path: String!) {
  repository(owner: $owner, name: $name) {
    object(expression: $ref) {
      ... on Commit {
        blame(path: $path) {
          ranges {
            startingLine
            endingLine
            commit {
              oid
              message
              author {
                name
                date
              }
            }
          }
        }
      }
    }
  }
}`

const githubGraphQLURL = "https://api.github.com/graphql"

func (t *ToolSet) runGetBlame(ctx context.Context, args map[string]any) (map[string]any, error) {
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

	// Default ref is "HEAD", which GitHub resolves to the repository's default
	// branch (main, master, or otherwise). Override if a ref is provided.
	ref := "HEAD"
	if r, ok := args["ref"].(string); ok && r != "" {
		ref = r
	}

	reqBody := graphQLRequest{
		Query: blameQuery,
		Variables: map[string]any{
			"owner": owner,
			"name":  repo,
			"ref":   ref,
			"path":  path,
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to marshal GraphQL request")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, githubGraphQLURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create GraphQL request")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "gollem-github-toolset")

	resp, err := t.client.DoGraphQL(ctx, httpReq)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to execute GraphQL request",
			goerr.V("owner", owner),
			goerr.V("repo", repo),
			goerr.V("path", path))
	}
	defer safe.Close(t.logger, resp.Body)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read GraphQL response")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, goerr.New("GraphQL request failed",
			goerr.V("status", resp.StatusCode),
			goerr.V("body", string(respBody)))
	}

	var gqlResp graphQLResponse
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		return nil, goerr.Wrap(err, "failed to parse GraphQL response")
	}

	if len(gqlResp.Errors) > 0 {
		return nil, goerr.New("GraphQL errors",
			goerr.V("errors", gqlResp.Errors))
	}

	if gqlResp.Data.Repository.Object == nil || gqlResp.Data.Repository.Object.Blame == nil {
		return nil, goerr.New("no blame data found",
			goerr.V("owner", owner),
			goerr.V("repo", repo),
			goerr.V("path", path),
			goerr.V("ref", ref))
	}

	raw := gqlResp.Data.Repository.Object.Blame.Ranges
	ranges := make([]BlameRange, 0, len(raw))
	for _, r := range raw {
		// Truncate long commit messages in a UTF-8-safe manner.
		message := r.Commit.Message
		if runes := []rune(message); len(runes) > 200 {
			message = string(runes[:200]) + "..."
		}

		ranges = append(ranges, BlameRange{
			StartLine:     r.StartingLine,
			EndLine:       r.EndingLine,
			CommitSHA:     r.Commit.OID,
			CommitMessage: message,
			Author:        r.Commit.Author.Name,
			Date:          r.Commit.Author.Date,
		})
	}

	return map[string]any{
		"repository": fmt.Sprintf("%s/%s", owner, repo),
		"path":       path,
		"ref":        ref,
		"ranges":     ranges,
		"count":      len(ranges),
	}, nil
}
