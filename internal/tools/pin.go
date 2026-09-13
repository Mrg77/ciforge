package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// usesRe captures a `uses:` line so the tag can be swapped for a SHA in place,
// preserving the file's formatting and comments.
var usesRe = regexp.MustCompile(`(?m)^(\s*(?:-\s*)?uses:\s*)([\w.-]+/[\w.-]+(?:/[\w./-]+)?)@([\w.-]+)(\s*(?:#.*)?)$`)

// resolveRef asks the GitHub API for the commit a tag or branch points at.
// Unauthenticated calls are rate-limited to 60/hour; GITHUB_TOKEN raises that.
func resolveRef(ctx context.Context, repo, ref string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/commits/%s", repo, ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github.sha")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	switch resp.StatusCode {
	case http.StatusOK:
		sha := strings.TrimSpace(string(body))
		if len(sha) != 40 {
			return "", fmt.Errorf("unexpected response for %s@%s", repo, ref)
		}
		return sha, nil
	case http.StatusForbidden, http.StatusTooManyRequests:
		return "", fmt.Errorf("GitHub API rate limit reached — set GITHUB_TOKEN to raise it")
	case http.StatusNotFound:
		return "", fmt.Errorf("%s@%s not found", repo, ref)
	default:
		return "", fmt.Errorf("GitHub API returned %d for %s@%s", resp.StatusCode, repo, ref)
	}
}

// PinActionsTool resolves every tag-pinned action to its commit SHA.
//
// A tag is a pointer. Whoever controls the action's repository can move it to
// different code, and your pipeline will run that code on the next push with no
// diff in your repository to show for it. A SHA cannot move.
//
// Read-only by design: it reports the substitutions rather than writing them.
// Rewriting a workflow is what the guard gates, and this tool is meant to be
// safe to run anywhere, including in CI.
type PinActionsTool struct{}

func (PinActionsTool) Name() string   { return "pin_actions" }
func (PinActionsTool) Danger() Danger { return ReadOnly }
func (PinActionsTool) Description() string {
	return "Resolve every tag-pinned third-party action to its full commit SHA and return the " +
		"exact replacement lines (SHA plus the original tag as a trailing comment). Reports the " +
		"changes; it does not write them — use write_file to apply, which is gated."
}
func (PinActionsTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "A specific workflow file. Default: every workflow."},
		},
		"additionalProperties": false,
	}
}

func (PinActionsTool) Run(ctx context.Context, input json.RawMessage) (string, error) {
	var in struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(input, &in)

	root := projectRoot
	if root == "" {
		root, _ = filepath.Abs(".")
	}
	var files []string
	if in.Path != "" {
		p, err := confine(in.Path)
		if err != nil {
			return "", err
		}
		files = []string{p}
	} else {
		var err error
		if files, err = workflowFiles(root); err != nil || len(files) == 0 {
			return "no workflows found under .github/workflows/", nil
		}
	}

	type change struct{ file, from, to string }
	var changes []change
	var problems []string
	seen := map[string]string{} // repo@ref -> sha, so a repeated action costs one call

	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(root, f)
		for _, m := range usesRe.FindAllStringSubmatch(string(b), -1) {
			repoPath, ref := m[2], m[3]
			if len(ref) == 40 {
				continue // already pinned
			}
			parts := strings.SplitN(repoPath, "/", 3)
			if len(parts) < 2 {
				continue
			}
			repo := parts[0] + "/" + parts[1]
			key := repo + "@" + ref
			sha, ok := seen[key]
			if !ok {
				c, cancel := context.WithTimeout(ctx, 15*time.Second)
				resolved, err := resolveRef(c, repo, ref)
				cancel()
				if err != nil {
					problems = append(problems, fmt.Sprintf("%s: %v", key, err))
					continue
				}
				seen[key] = resolved
				sha = resolved
			}
			changes = append(changes, change{
				file: rel,
				from: fmt.Sprintf("uses: %s@%s", repoPath, ref),
				to:   fmt.Sprintf("uses: %s@%s # %s", repoPath, sha, ref),
			})
		}
	}

	if len(changes) == 0 && len(problems) == 0 {
		return "every action is already pinned to a commit SHA.", nil
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].file < changes[j].file })

	var b strings.Builder
	fmt.Fprintf(&b, "%d action(s) to pin:\n\n", len(changes))
	for _, c := range changes {
		fmt.Fprintf(&b, "%s\n  -  %s\n  +  %s\n\n", c.file, c.from, c.to)
	}
	if len(problems) > 0 {
		fmt.Fprintf(&b, "could not resolve (say so plainly rather than guessing):\n")
		for _, p := range problems {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
	}
	return truncate(b.String(), 10000), nil
}
