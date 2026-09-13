package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/Mrg77/ciforge/internal/agent"
	"github.com/Mrg77/ciforge/internal/anthropic"
	"github.com/Mrg77/ciforge/internal/guard"
	"github.com/Mrg77/ciforge/internal/report"
	"github.com/Mrg77/ciforge/internal/tools"
	"github.com/Mrg77/ciforge/internal/trace"
)

// runFix repairs what the deterministic rules found, then re-checks.
//
// Narrower here than in the other tools, deliberately. A workflow file is the
// shortest path to production, and the guard already refuses to rewrite a deploy
// or release workflow. A fix mode that worked around its own policy to look more
// capable would be worse than one that stops and says why — so this fixes what
// is mechanical and reversible, and reports the rest.
func runFix(args []string) int {
	fs := flag.NewFlagSet("fix", flag.ExitOnError)
	failOn := fs.String("fail-on", "high", "Exit 1 when a finding at this severity or above SURVIVES the fix.")
	showDiff := fs.Bool("diff", false, "Print what changed on disk.")
	maxTurns := fs.Int("max-turns", 25, "Bound the agent loop.")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `ciforge fix — repair workflow findings, then re-check.

Fixes what is mechanical: pinning actions to a SHA, narrowing permissions,
adding a concurrency group. It will NOT touch a deploy or release workflow —
that is policy, not a limitation to work around — and it never triggers a run.

Usage:
  ciforge fix [flags]

Flags:`)
		fs.PrintDefaults()
	}
	report.ParseArgs(fs, args)

	wd, _ := os.Getwd()
	tools.SetProjectRoot(wd)

	before, err := tools.Audit()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ciforge fix:", err)
		return 2
	}
	if len(before) == 0 {
		fmt.Println("nothing to fix — the audit is clean.")
		return 0
	}
	fmt.Printf("ciforge fix · %d finding(s) to work through\n\n", len(before))

	client, err := anthropic.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ciforge fix:", err)
		return 2
	}

	// Read, resolve SHAs, edit, re-audit. pin_actions is what makes the main
	// finding fixable without the model inventing a hash.
	toolset := []tools.Tool{
		tools.ListWorkflowsTool{},
		tools.ReadFileTool{},
		tools.PinActionsTool{},
		tools.EditFileTool{},
		tools.AuditTool{},
		tools.ActionlintTool{},
	}

	// No approver: unattended, so a "confirm" decision fails closed and a deploy
	// workflow simply cannot be written.
	g := guard.New(guard.Default(), nil)

	tr, err := trace.New(client.Model(), auditLogPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "ciforge fix:", err)
		return 2
	}
	defer tr.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ag := agent.New(client, fixPrompt, toolset, g, tr, os.Stdout)
	if v := os.Getenv("CIFORGE_MAX_COST"); v != "" {
		if usd, err := strconv.ParseFloat(v, 64); err == nil && usd > 0 {
			ag.SetBudget(usd)
		}
	}

	task := "Fix the workflow findings. Run workflow_audit first to see them, use pin_actions " +
		"for any unpinned action so you never invent a SHA, apply the fixes with edit_file, then " +
		"run workflow_audit again to confirm. Leave anything the policy refuses and say why."
	runErr := ag.Run(ctx, task, *maxTurns)
	fmt.Fprintln(os.Stderr, "\n"+tr.Summary())
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "ciforge fix:", runErr)
	}

	// Re-check with the deterministic rules, not with the model's account of
	// its own work.
	after, err := tools.Audit()
	if err != nil {
		return 2
	}
	r := &report.Report{Tool: "ciforge fix", Subject: "workflows", Findings: after,
		Scanned: fmt.Sprintf("%d finding(s) before · %d after", len(before), len(after))}
	fmt.Print(r.Text(0))

	if *showDiff {
		cmd := exec.CommandContext(ctx, "git", "diff", "--stat")
		cmd.Dir = wd
		if out, err := cmd.CombinedOutput(); err == nil && len(out) > 0 {
			fmt.Printf("\nchanges on disk:\n%s\n", out)
		}
	}
	return r.ExitCode(report.Severity(*failOn))
}

const fixPrompt = `You are ciforge in unattended fix mode. You repair GitHub Actions workflows
that a deterministic audit has flagged, and you prove the repair by re-running the audit.

Work one finding at a time:
  1. read_file the workflow before editing it, so your edit matches exactly.
  2. edit_file for a surgical change — one replacement, reviewable on its own.
  3. Run workflow_audit again to confirm the finding is gone.

What you fix, because it is mechanical and reversible:
  - an action pinned to a tag: call pin_actions and paste the SHA it returns, keeping the
    tag as a trailing comment. NEVER write a SHA you did not get from pin_actions.
  - a missing top-level permissions block: add contents: read, then raise per job only what
    that job actually needs (pull-requests: write to comment, id-token: write for OIDC).
  - a missing concurrency group on a push/pull_request workflow.

What you do NOT do:
  - You cannot edit a deploy or release workflow. The policy refuses it and that is the
    point, not an obstacle: report the finding and explain that a human must make that
    change. Do not rename, copy, or route around the file to get near it.
  - You never trigger a workflow run.
  - You do not migrate credentials to OIDC unattended: that needs a trust policy in the
    cloud account, and half a migration is worse than none. Report it for a human.
  - You do not invent a SHA, a version, or an action name.

Finish with two short lists: what you fixed, and what you left with the reason.`
