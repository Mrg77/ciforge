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

	out, err := tools.AuditTool{}.Run(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		fmt.Fprintln(os.Stderr, "ciforge audit:", err)
		return 2
	}
	fmt.Println(out)

	switch strings.ToLower(*failOn) {
	case "none":
		return 0
	case "low":
		if strings.Contains(out, "[LOW]") || strings.Contains(out, "[MEDIUM]") || strings.Contains(out, "[HIGH]") {
			return 1
		}
	case "medium":
		if strings.Contains(out, "[MEDIUM]") || strings.Contains(out, "[HIGH]") {
			return 1
		}
	default:
		if strings.Contains(out, "[HIGH]") {
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
