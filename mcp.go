package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Mrg77/ciforge/internal/mcp"
	"github.com/Mrg77/ciforge/internal/report"
	"github.com/Mrg77/ciforge/internal/tools"
)

// runMCP serves the deterministic checks over the Model Context Protocol.
//
// Only the read-only half is exposed, and it matters more here than elsewhere:
// a workflow file is the shortest path to production. An MCP server is driven by
// a model without a human approving each call, so exposing `fix` would let an
// agent rewrite a pipeline through a channel where the guard never runs. `pin`
// is included because it only resolves SHAs and returns them — it writes
// nothing.
func runMCP(args []string) int {
	root := "."
	if len(args) > 0 {
		root = args[0]
	}
	if abs, err := os.Getwd(); err == nil && root == "." {
		root = abs
	}
	tools.SetProjectRoot(root)

	noArgs := map[string]any{"type": "object", "additionalProperties": false}

	audit := func(_ map[string]any) (string, any, error) {
		findings, err := tools.Audit()
		if err != nil {
			return "", nil, err
		}
		r := &report.Report{Tool: "ciforge", Subject: "workflows", Findings: findings}
		r.Sort()
		var out struct {
			Count       int              `json:"count"`
			MaxSeverity string           `json:"max_severity"`
			Findings    []report.Finding `json:"findings"`
		}
		out.Count = len(findings)
		out.MaxSeverity = string(r.Worst())
		if out.MaxSeverity == "" {
			out.MaxSeverity = "none"
		}
		out.Findings = r.Findings
		var structured any
		b, _ := json.Marshal(out)
		_ = json.Unmarshal(b, &structured)
		return r.Text(0), structured, nil
	}

	pin := func(_ map[string]any) (string, any, error) {
		out, err := tools.PinActionsTool{}.Run(nil, []byte(`{}`))
		if err != nil {
			return "", nil, err
		}
		return out, nil, nil
	}

	srv := &mcp.Server{
		Name:    "ciforge",
		Version: version,
		Tools: []mcp.Tool{
			{
				Name: "workflow_audit",
				Description: "Audit every GitHub Actions workflow in the repository: actions pinned to a tag " +
					"rather than a commit SHA, pull_request_target combined with a checkout of the PR, event data " +
					"interpolated into a run: block, over-broad permissions, long-lived cloud credentials. " +
					"Deterministic and read-only — it changes nothing.",
				InputSchema: noArgs,
				Run:         audit,
			},
			{
				Name: "workflow_pin",
				Description: "Resolve every tag-pinned action to its full commit SHA and return the exact " +
					"replacement lines. Reads the GitHub API; writes nothing. Use this instead of guessing a SHA — " +
					"an invented one is worse than an unpinned action.",
				InputSchema: noArgs,
				Run:         pin,
			},
		},
	}

	fmt.Fprintf(os.Stderr, "ciforge mcp · serving %d read-only tool(s) on stdio · root %s\n", len(srv.Tools), root)
	return srv.Run()
}
