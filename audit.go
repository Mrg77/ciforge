package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Mrg77/ciforge/internal/report"
	"github.com/Mrg77/ciforge/internal/tools"
)

// runScan gates the workflows: the deterministic half. No model, no API key,
// and the same verdict on the same input — the only kind of check that belongs
// in a pipeline. The agent advises; this decides.
func runScan(args []string) int {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	var o report.Options
	o.Bind(fs, "high")
	fs.Usage = usageFor(fs, "scan", "Deterministic audit of .github/workflows. Exits 1 at --fail-on or above.")
	report.ParseArgs(fs, args)
	return emit(o)
}

// runAudit is the same checks, reported rather than gated: report-only by
// default, with the per-category breakdown that becomes the HTML tabs.
func runAudit(args []string) int {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	var o report.Options
	o.Bind(fs, "none")
	fs.Usage = usageFor(fs, "audit", "Prioritised report of every workflow. Report-only unless --fail-on is given.")
	report.ParseArgs(fs, args)
	return emit(o)
}

// emit runs the rules and renders. Both subcommands share it, so scan and audit
// can never disagree about what a finding is.
func emit(o report.Options) int {
	wd, _ := os.Getwd()
	tools.SetProjectRoot(wd)

	findings, err := tools.Audit()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ciforge:", err)
		return 2
	}

	r := &report.Report{
		Tool:     "ciforge",
		Subject:  filepath.Base(wd),
		Scanned:  scannedSummary(wd),
		Findings: findings,
	}
	if o.Explain {
		if err := explain(r); err != nil {
			// The deterministic findings stand on their own; a failed
			// explanation annotates nothing but discards nothing either.
			fmt.Fprintln(os.Stderr, "ciforge: --explain failed, reporting without it:", err)
		}
	}
	return r.Emit(o)
}

// scannedSummary states what was actually looked at, so a report on a repo with
// no workflows cannot be mistaken for a clean bill of health.
func scannedSummary(root string) string {
	files, _ := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.y*ml"))
	if len(files) == 0 {
		return "no workflows found under .github/workflows/"
	}
	return fmt.Sprintf("scanned %d workflow file(s)", len(files))
}

// runPin prints the SHA replacement for every tag-pinned action. Read-only: it
// shows the change, it does not make it — rewriting a workflow is gated.
func runPin(args []string) int {
	fs := flag.NewFlagSet("pin", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "Machine-readable output.")
	report.ParseArgs(fs, args)

	wd, _ := os.Getwd()
	tools.SetProjectRoot(wd)

	out, err := tools.PinActionsTool{}.Run(nil, []byte(`{}`))
	if err != nil {
		fmt.Fprintln(os.Stderr, "ciforge pin:", err)
		return 2
	}
	if *asJSON {
		fmt.Printf("{%q:%q}\n", "report", out)
	} else {
		fmt.Println(out)
	}
	return 0
}

func usageFor(fs *flag.FlagSet, name, what string) func() {
	return func() {
		fmt.Fprintf(os.Stderr, "ciforge %s — %s\n\nUsage:\n  ciforge %s [flags]\n\nFlags:\n", name, what, name)
		fs.PrintDefaults()
	}
}
