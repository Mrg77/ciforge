package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Per-minute price of a GitHub-hosted Linux runner on paid plans, in USD.
// Public repositories are free; the figure exists to turn "this is wasteful"
// into a number someone can weigh against the cost of maintaining a fix.
const linuxMinuteUSD = 0.008

// CostTool turns pipeline waste into euros per month.
//
// "Add a cache" is a preference. "This installs dependencies from scratch on
// every run; caching saves roughly 40 minutes a day" is an argument. Estimates
// are labelled as estimates — a number presented as a measurement when it is a
// guess destroys trust in every other number.
type CostTool struct{}

func (CostTool) Name() string   { return "cost_estimate" }
func (CostTool) Danger() Danger { return ReadOnly }
func (CostTool) Description() string {
	return "Estimate wasted CI minutes and their monthly cost: dependency installs without a " +
		"cache, missing concurrency groups, heavy jobs with no path filter, oversized runners. " +
		"Returns estimates, clearly labelled as such."
}
func (CostTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"runs_per_day": map[string]any{"type": "number", "description": "Observed pipeline runs per day. Default: 10."},
		},
		"additionalProperties": false,
	}
}

func (CostTool) Run(_ context.Context, input json.RawMessage) (string, error) {
	var in struct {
		RunsPerDay float64 `json:"runs_per_day"`
	}
	_ = json.Unmarshal(input, &in)
	if in.RunsPerDay <= 0 {
		in.RunsPerDay = 10
	}

	root := projectRoot
	if root == "" {
		root, _ = filepath.Abs(".")
	}
	files, err := workflowFiles(root)
	if err != nil || len(files) == 0 {
		return "no workflows found under .github/workflows/", nil
	}

	type opportunity struct {
		file, what, fix string
		minutesPerRun   float64
	}
	var opps []opportunity

	for _, f := range files {
		rel, _ := filepath.Rel(root, f)
		w, err := parse(f)
		if err != nil {
			continue
		}
		trigs := w.triggers()

		if w.Concurrency == nil && (contains(trigs, "push") || contains(trigs, "pull_request")) {
			// Rough: one superseded run in five is cancelled by a concurrency group.
			opps = append(opps, opportunity{rel, "no concurrency group — superseded runs finish anyway", "Add a concurrency group keyed on the ref with cancel-in-progress outside the default branch.", 2})
		}

		jobs := make([]string, 0, len(w.Jobs))
		for n := range w.Jobs {
			jobs = append(jobs, n)
		}
		sort.Strings(jobs)

		for _, name := range jobs {
			j := w.Jobs[name]
			cached := false
			installs := 0
			for _, s := range j.Steps {
				if strings.Contains(s.Uses, "actions/cache") {
					cached = true
				}
				if c, ok := s.With["cache"]; ok && fmt.Sprint(c) != "false" {
					cached = true // setup-* actions with built-in caching
				}
				r := strings.ToLower(s.Run)
				if strings.Contains(r, "npm ci") || strings.Contains(r, "npm install") ||
					strings.Contains(r, "pip install") || strings.Contains(r, "go mod download") ||
					strings.Contains(r, "bundle install") || strings.Contains(r, "apt-get install") {
					installs++
				}
			}
			if installs > 0 && !cached {
				opps = append(opps, opportunity{
					rel + " · job " + name,
					fmt.Sprintf("%d dependency install(s) with no cache", installs),
					"Add actions/cache, or enable the cache option of the matching setup-* action.",
					float64(installs) * 1.5,
				})
			}
		}
	}

	if len(opps) == 0 {
		return "cost_estimate: nothing obviously wasteful. (Static reading of the workflows; real timings come from the Actions usage page.)", nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "cost_estimate — ESTIMATES, at %.0f runs/day and $%.3f per Linux minute\n"+
		"(free on public repositories; real timings live in the Actions usage page)\n\n",
		in.RunsPerDay, linuxMinuteUSD)

	total := 0.0
	for _, o := range opps {
		perMonth := o.minutesPerRun * in.RunsPerDay * 30
		cost := perMonth * linuxMinuteUSD
		total += cost
		fmt.Fprintf(&b, "%s\n  %s\n  ~%.0f min/month  ≈ $%.2f/month\n  fix: %s\n\n",
			o.file, o.what, perMonth, cost, o.fix)
	}
	fmt.Fprintf(&b, "total addressable: ≈ $%.2f/month.\n\n"+
		"Not every optimisation is worth its maintenance. Weigh each against the cost of keeping it.\n", total)
	return truncate(b.String(), 8000), nil
}
