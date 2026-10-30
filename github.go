package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// prFragment is the set of fields every query fetches, so a PR found by
// search and a PR found by URL lookup carry the same data.
const prFragment = `
fragment pr on PullRequest {
  number title url isDraft state mergedAt closedAt body
  repository { nameWithOwner isPrivate }
  author { login }
  reviews(last: 50) { nodes { author { login } body } }
  reviewThreads(first: 100) { totalCount nodes { isResolved } }
}`

type actor struct{ Login string }

// pullRequest mirrors prFragment. encoding/json matches field names
// case-insensitively, so no struct tags are needed.
type pullRequest struct {
	Number     int
	Title      string
	URL        string
	IsDraft    bool
	State      string // OPEN, CLOSED, MERGED
	MergedAt   string
	ClosedAt   string
	Body       string
	Repository struct {
		NameWithOwner string
		IsPrivate     bool
	}
	Author  actor
	Reviews struct {
		Nodes []struct {
			Author actor
			Body   string
		}
	}
	ReviewThreads struct {
		TotalCount int
		Nodes      []struct{ IsResolved bool }
	}
}

func (p *pullRequest) endedAt() string {
	if p.MergedAt != "" {
		return p.MergedAt
	}
	return p.ClosedAt
}

func (p *pullRequest) unresolvedThreads() int {
	n := 0
	for _, t := range p.ReviewThreads.Nodes {
		if !t.IsResolved {
			n++
		}
	}
	return n
}

// github talks to GitHub through the gh CLI, reusing its authentication.
type github struct {
	cache map[string]*pullRequest // by URL; nil value means "not a PR"
}

func newGitHub() *github {
	return &github{cache: map[string]*pullRequest{}}
}

func (g *github) graphql(query string, vars map[string]string, out any) error {
	args := []string{"api", "graphql", "-f", "query=" + query}
	for k, v := range vars {
		args = append(args, "-f", k+"="+v)
	}
	cmd := exec.Command("gh", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()

	var resp struct {
		Data   json.RawMessage
		Errors []struct{ Message string }
	}
	if jerr := json.Unmarshal(b, &resp); jerr != nil {
		if err != nil {
			return fmt.Errorf("gh api graphql: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return fmt.Errorf("decoding gh output: %w", jerr)
	}
	if len(resp.Errors) > 0 {
		msgs := make([]string, len(resp.Errors))
		for i, e := range resp.Errors {
			msgs[i] = e.Message
		}
		return fmt.Errorf("graphql: %s", strings.Join(msgs, "; "))
	}
	if err != nil {
		return fmt.Errorf("gh api graphql: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return json.Unmarshal(resp.Data, out)
}

func (g *github) login() (string, error) {
	var data struct{ Viewer actor }
	if err := g.graphql(`query { viewer { login } }`, nil, &data); err != nil {
		return "", err
	}
	return data.Viewer.Login, nil
}

// search returns every PR matching q, following pagination.
func (g *github) search(q string) ([]*pullRequest, error) {
	query := `query($q: String!, $after: String) {
  search(query: $q, type: ISSUE, first: 50, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes { ...pr }
  }
}` + prFragment

	var all []*pullRequest
	after := ""
	for {
		vars := map[string]string{"q": q}
		if after != "" {
			vars["after"] = after
		}
		var data struct {
			Search struct {
				PageInfo struct {
					HasNextPage bool
					EndCursor   string
				}
				Nodes []*pullRequest
			}
		}
		if err := g.graphql(query, vars, &data); err != nil {
			return nil, fmt.Errorf("search %q: %w", q, err)
		}
		for _, p := range data.Search.Nodes {
			if p != nil && p.URL != "" {
				g.cache[p.URL] = p
				all = append(all, p)
			}
		}
		if !data.Search.PageInfo.HasNextPage {
			return all, nil
		}
		after = data.Search.PageInfo.EndCursor
	}
}

// lookup fetches a single PR by URL. It returns nil, nil if the URL does not
// resolve to a pull request.
func (g *github) lookup(url string) (*pullRequest, error) {
	if p, ok := g.cache[url]; ok {
		return p, nil
	}
	query := `query($u: URI!) { resource(url: $u) { ...pr } }` + prFragment
	var data struct{ Resource *pullRequest }
	if err := g.graphql(query, map[string]string{"u": url}, &data); err != nil {
		return nil, fmt.Errorf("lookup %s: %w", url, err)
	}
	p := data.Resource
	if p != nil && p.URL == "" {
		p = nil
	}
	g.cache[url] = p
	return p, nil
}
