package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Mrg77/ciforge/internal/tools"
)

// runAudit is the CI subcommand: deterministic, no LLM, no API key. A gate must
// return the same verdict on the same input, today and in six months — which is
// exactly what a model cannot promise. The agent advises; this decides.
func runAudit(args []string) int {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	failOn := fs.String("fail-on", "high", "Exit non-zero at this severity or above: high, medium, low, none.")
	asJSON := fs.Bool("json", false, "Emit findings as JSON, for aggregation by another tool.")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `ciforge audit — deterministic workflow audit (no LLM, no API key).

Usage:
  ciforge audit [flags]

Flags:`)
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	wd, _ := os.Getwd()
	tools.SetProjectRoot(wd)

	findings, err := tools.Audit()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ciforge audit:", err)
		return 2
	}
	if *asJSON {
		out, _ := json.MarshalIndent(map[string]any{
			"tool":         "ciforge",
			"findings":     findings,
			"count":        len(findings),
			"max_severity": tools.MaxSeverity(findings),
		}, "", "  ")
		fmt.Println(string(out))
	} else {
		fmt.Print(tools.Render("workflow_audit", findings))
	}

	// Decide on counts, never by grepping the rendered text: the report is
	// coloured, and a gate must not depend on how something is displayed.
	rank := map[string]int{"low": 1, "medium": 2, "high": 3}
	threshold := rank[strings.ToLower(*failOn)]
	if strings.EqualFold(*failOn, "none") {
		return 0
	}
	if threshold == 0 {
		threshold = rank["high"]
	}
	for _, f := range findings {
		if rank[f.Severity] >= threshold {
			return 1
		}
	}
	return 0
}

// runPin prints the SHA replacement for every tag-pinned action. Read-only: it
// shows the change, it does not make it.
func runPin(args []string) int {
	fs := flag.NewFlagSet("pin", flag.ExitOnError)
	_ = fs.Parse(args)

	wd, _ := os.Getwd()
	tools.SetProjectRoot(wd)

	out, err := tools.PinActionsTool{}.Run(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		fmt.Fprintln(os.Stderr, "ciforge pin:", err)
		return 2
	}
	fmt.Println(out)
	return 0
}
