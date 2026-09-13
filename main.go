// Command ciforge is an AI agent that hardens and optimises GitHub Actions.
//
// A CI runner often holds more rights than the engineer who wrote the pipeline:
// it deploys, it reads secrets, it pushes images. And it executes third-party
// code on a single `uses:` line. ciforge treats that asymmetry as what it is —
// a security surface — and reports it with the fix attached.
//
// The agent advises. It never blocks a pipeline and it never triggers one:
// a gate must be reproducible, and a model is not.
//
// Usage:
//
//	export ANTHROPIC_API_KEY=...   # from console.anthropic.com (billed per token)
//	ciforge "audit my workflows and pin every third-party action"
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/Mrg77/ciforge/internal/agent"
	"github.com/Mrg77/ciforge/internal/anthropic"
	"github.com/Mrg77/ciforge/internal/guard"
	"github.com/Mrg77/ciforge/internal/tools"
	"github.com/Mrg77/ciforge/internal/trace"
)

var version = "dev"

const systemPrompt = `You are ciforge, an AI agent that hardens and optimises GitHub
Actions workflows for a DevOps engineer.

Start from this asymmetry: a CI runner usually has more rights than the engineer who
wrote the pipeline — it deploys, reads secrets, pushes images — and it runs third-party
code on a single "uses:" line. Treat the pipeline as a security surface.

Your loop:

  1. READ — workflow_list, then read_file on anything you intend to change. Never
     propose a change to a workflow you have not read.
  2. AUDIT — workflow_audit for the deterministic findings, actionlint when installed.
  3. PRIORITISE — supply chain and secret exposure first, permissions next, cost last.
     Do not bury a high finding under a list of style notes.
  4. PROPOSE — the exact replacement lines. pin_actions gives you the SHA for each
     tag-pinned action.
  5. APPLY — only when asked, with edit_file, one change at a time so each is reviewable.
     Writing a workflow is gated by policy; writing a deployment or release workflow is
     refused outright. Do not attempt to work around that: explain and hand it back.

What you always check:
  - Third-party actions pinned to a tag rather than a commit SHA. A tag is a pointer:
    it can be moved to different code and your pipeline runs it with no diff in the repo.
  - pull_request_target combined with a checkout of the PR head — secrets exposed to
    code from a fork, the documented exfiltration path.
  - ${{ github.event.* }} interpolated into a run: block — a PR title is user input,
    and inside a shell line it is code. The fix is passing it through env:.
  - Top-level permissions. Unset means the repository default, usually write. Almost no
    job needs to write to the repo: set contents: read, raise per job.
  - Long-lived cloud keys where OIDC would do — and when proposing OIDC, insist the
    trust policy is restricted to the repo AND the branch or environment. A wildcard
    subject hands the role to every repository in the org, which undoes the point.

Honesty rules:
  - When a tool did not run (actionlint absent, API rate limit reached), say the check
    did NOT happen. Silence must never read as success.
  - Cost figures are estimates. Label them as estimates.
  - Not every optimisation is worth its maintenance. Say when one is not.`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, `ciforge — an AI agent that hardens and optimises GitHub Actions.

Usage:
  ciforge "<task>"           run the agent on a task
  ciforge audit [--fail-on]  deterministic workflow audit (no LLM, no API key)
  ciforge pin                show the SHA for every tag-pinned action
  ciforge version

Environment:
  ANTHROPIC_API_KEY          required for agent runs
  GITHUB_TOKEN               raises the GitHub API rate limit when resolving SHAs
  CIFORGE_MODEL              override the model
  CIFORGE_MAX_COST           stop before exceeding this spend, in USD
  CIFORGE_AUDIT              audit log path, or "off"

Examples:
  ciforge "audit my workflows and pin every third-party action"
  ciforge "reduce permissions to the minimum each job needs"
  ciforge audit --fail-on high`)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Println("ciforge", version)
		return
	case "audit":
		// Deterministic: no LLM, no API key, same verdict every time. That is
		// what lets it gate a pipeline — unlike the agent, which advises.
		os.Exit(runAudit(os.Args[2:]))
	case "pin":
		os.Exit(runPin(os.Args[2:]))
	}

	task := strings.Join(os.Args[1:], " ")

	client, err := anthropic.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ciforge:", err)
		os.Exit(1)
	}
	if wd, err := os.Getwd(); err == nil {
		tools.SetProjectRoot(wd)
	}

	toolset := []tools.Tool{
		tools.ListWorkflowsTool{}, // read the ground before proposing
		tools.ReadFileTool{},
		tools.AuditTool{},      // deterministic findings
		tools.ActionlintTool{}, // the reference linter, when present
		tools.PinActionsTool{}, // tag -> SHA
		tools.CostTool{},       // waste, priced
		tools.EditFileTool{},   // gated: a workflow is the deploy path
		tools.WriteFileTool{},  // gated
	}

	g := guard.New(guard.Default(), ttyConfirm)

	tr, err := trace.New(client.Model(), auditLogPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "ciforge: could not open audit log:", err)
		os.Exit(1)
	}
	defer tr.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ag := agent.New(client, systemPrompt, toolset, g, tr, os.Stdout)
	if v := os.Getenv("CIFORGE_MAX_COST"); v != "" {
		if usd, err := strconv.ParseFloat(v, 64); err == nil && usd > 0 {
			ag.SetBudget(usd)
		}
	}

	fmt.Printf("ciforge · model %s\n\n", client.Model())
	runErr := ag.Run(ctx, task, 30)

	fmt.Fprintln(os.Stderr, "\n"+tr.Summary())
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "ciforge:", runErr)
		os.Exit(1)
	}
}

func auditLogPath() string {
	switch v := os.Getenv("CIFORGE_AUDIT"); v {
	case "off", "0", "false":
		return ""
	case "":
		return trace.DefaultLogPath()
	default:
		return v
	}
}

// ttyConfirm asks the human to approve a guarded action. Without a terminal it
// returns false — an agent does not get to edit a workflow unattended.
func ttyConfirm(action, ctx, message string) bool {
	fmt.Fprintf(os.Stderr, "\n⚠  The agent wants to: %s", action)
	if ctx != "" {
		fmt.Fprintf(os.Stderr, "  (context: %s)", ctx)
	}
	fmt.Fprintf(os.Stderr, "\n   %s\n   Proceed? [y/N] ", message)

	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(sc.Text()))
	return answer == "y" || answer == "yes"
}
