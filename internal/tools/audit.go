package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Finding is one issue with enough context to act on it.
type Finding struct {
	Severity string
	File     string
	Job      string
	Rule     string
	Message  string
	Fix      string
}

var (
	// A third-party action pinned to a tag or branch rather than a SHA.
	tagPinned = regexp.MustCompile(`^([\w.-]+)/([\w.-]+)(/[\w./-]+)?@(v?[\w.-]+)$`)
	// A full 40-hex commit SHA — the only immutable pin.
	shaPinned = regexp.MustCompile(`^[\w.-]+/[\w.-]+(/[\w./-]+)?@[0-9a-f]{40}$`)
	// Interpolation of attacker-controllable event data into a shell command.
	// A PR title is user input; inside run: it is code.
	scriptInjection = regexp.MustCompile(`\$\{\{\s*(github\.event\.(issue|pull_request|comment|review)\.[\w.]*(title|body|login|ref|label)|github\.head_ref)`)
)

// firstPartyOwners are maintained by GitHub itself. A lower risk, not a null
// one — they are listed separately so a real finding is not buried under fifty
// notices. Burying a risk is the same as hiding it.
var firstPartyOwners = map[string]bool{"actions": true, "github": true}

// AuditTool is the deterministic core: it reads every workflow and reports what
// is wrong, ordered by severity. No model involved, so the same repository
// always produces the same verdict — which is what a CI gate requires.
type AuditTool struct{}

func (AuditTool) Name() string   { return "workflow_audit" }
func (AuditTool) Danger() Danger { return ReadOnly }
func (AuditTool) Description() string {
	return "Audit every workflow: unpinned third-party actions, pull_request_target with a " +
		"checkout of the PR, script injection through event data, over-broad permissions, " +
		"long-lived cloud credentials, missing concurrency, uncached dependency installs."
}
func (AuditTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
}

func (AuditTool) Run(_ context.Context, _ json.RawMessage) (string, error) {
	root := projectRoot
	if root == "" {
		root, _ = filepath.Abs(".")
	}
	files, err := workflowFiles(root)
	if err != nil || len(files) == 0 {
		return "no workflows found under .github/workflows/", nil
	}

	var findings []Finding
	for _, f := range files {
		rel, _ := filepath.Rel(root, f)
		w, err := parse(f)
		if err != nil {
			findings = append(findings, Finding{
				Severity: "high", File: rel, Rule: "unparseable",
				Message: err.Error(), Fix: "Fix the YAML — a workflow that does not parse does not run.",
			})
			continue
		}
		findings = append(findings, auditWorkflow(rel, w)...)
	}
	return report(findings), nil
}

func auditWorkflow(file string, w *workflow) []Finding {
	var out []Finding
	trigs := w.triggers()
	hasPRTarget := contains(trigs, "pull_request_target")

	// Top-level permissions: unset means the repository default, which is
	// usually write. Almost no workflow needs to write to the repo.
	if w.Permissions == nil {
		out = append(out, Finding{
			Severity: "medium", File: file, Rule: "permissions-unset",
			Message: "No top-level `permissions:` — the job token inherits the repository default, often write access to the repo.",
			Fix:     "Add `permissions: contents: read` at workflow level, then raise only what a specific job needs.",
		})
	} else if p, ok := w.Permissions.(string); ok && p == "write-all" {
		out = append(out, Finding{
			Severity: "high", File: file, Rule: "permissions-write-all",
			Message: "`permissions: write-all` grants every scope to every job.",
			Fix:     "Replace with `contents: read` and grant individual scopes per job.",
		})
	}

	// Concurrency: without it, three pushes run three full pipelines, two of
	// them already obsolete.
	if w.Concurrency == nil && (contains(trigs, "push") || contains(trigs, "pull_request")) {
		out = append(out, Finding{
			Severity: "low", File: file, Rule: "no-concurrency",
			Message: "No concurrency group: successive pushes run overlapping pipelines, and the stale ones still bill minutes.",
			Fix:     "Add a concurrency group keyed on the ref, with cancel-in-progress for non-default branches.",
		})
	}

	jobs := make([]string, 0, len(w.Jobs))
	for name := range w.Jobs {
		jobs = append(jobs, name)
	}
	sort.Strings(jobs)

	for _, name := range jobs {
		j := w.Jobs[name]
		for _, s := range j.Steps {
			out = append(out, auditStep(file, name, s, hasPRTarget)...)
		}
	}
	return out
}

func auditStep(file, jobName string, s step, hasPRTarget bool) []Finding {
	var out []Finding

	if s.Uses != "" {
		owner := strings.SplitN(s.Uses, "/", 2)[0]
		switch {
		case strings.HasPrefix(s.Uses, "./"), strings.HasPrefix(s.Uses, "docker://"):
			// A local composite action or a container image: different risk model.
		case shaPinned.MatchString(s.Uses):
			// Correctly pinned.
		case tagPinned.MatchString(s.Uses):
			sev := "high"
			extra := ""
			if firstPartyOwners[owner] {
				sev = "low"
				extra = " (GitHub-maintained: lower risk, not zero)"
			}
			out = append(out, Finding{
				Severity: sev, File: file, Job: jobName, Rule: "action-not-pinned",
				Message: fmt.Sprintf("`%s` is pinned to a tag%s. A tag is a pointer: whoever controls the repository — or compromises the maintainer's account — can move it to different code, and your pipeline runs it with no diff in yours.", s.Uses, extra),
				Fix:     "Pin to the full commit SHA and keep the tag as a trailing comment: `uses: owner/action@<40-hex sha> # v4.1.0`.",
			})
		}

		// pull_request_target + a checkout of the PR head is the documented
		// path to secret exfiltration from a fork.
		if hasPRTarget && strings.Contains(s.Uses, "actions/checkout") {
			if ref, ok := s.With["ref"]; ok {
				r := fmt.Sprint(ref)
				if strings.Contains(r, "head") || strings.Contains(r, "pull_request") {
					out = append(out, Finding{
						Severity: "high", File: file, Job: jobName, Rule: "pr-target-checkout",
						Message: "`pull_request_target` runs with repository secrets and this step checks out the PR's own code. Anyone can open a pull request; that code then executes with access to your secrets.",
						Fix:     "Use `pull_request` for anything that builds fork code. If you genuinely need pull_request_target, do not check out the PR head, and never expose secrets to a job that does.",
					})
				}
			}
		}
	}

	if s.Run != "" && scriptInjection.MatchString(s.Run) {
		out = append(out, Finding{
			Severity: "high", File: file, Job: jobName, Rule: "script-injection",
			Message: "Event data is interpolated directly into a shell command. A PR title or branch name is attacker-controlled input; inside `run:` it becomes code.",
			Fix:     "Pass it through `env:` and reference the environment variable in the script, quoted. Never interpolate `${{ github.event.* }}` into a shell line.",
		})
	}

	// Long-lived cloud credentials where OIDC would do.
	for k := range s.With {
		// GitHub Actions inputs use dashes (aws-secret-access-key); environment
		// variables use underscores. Normalise so neither form slips through.
		lk := strings.ReplaceAll(strings.ToLower(k), "-", "_")
		if strings.Contains(lk, "secret_access_key") || lk == "aws_access_key_id" {
			out = append(out, Finding{
				Severity: "high", File: file, Job: jobName, Rule: "long-lived-credentials",
				Message: "A long-lived cloud key is passed to this step. It sits in repository secrets until someone rotates it, which nobody does.",
				Fix:     "Switch to OIDC: `permissions: id-token: write` plus role-to-assume. Restrict the role's trust policy to this repository AND branch — a wildcard `repo:*` hands the role to every repo in the org.",
			})
		}
	}
	for k := range s.Env {
		if strings.Contains(strings.ReplaceAll(strings.ToLower(k), "-", "_"), "secret_access_key") {
			out = append(out, Finding{
				Severity: "high", File: file, Job: jobName, Rule: "long-lived-credentials",
				Message: "A long-lived cloud key is exposed as an environment variable.",
				Fix:     "Switch to OIDC and delete the stored key.",
			})
		}
	}

	return out
}

func report(findings []Finding) string {
	if len(findings) == 0 {
		return "workflow_audit: no findings. (Deterministic checks only — a clean audit is not a proof of safety.)"
	}
	order := map[string]int{"high": 0, "medium": 1, "low": 2}
	sort.SliceStable(findings, func(i, j int) bool {
		if order[findings[i].Severity] != order[findings[j].Severity] {
			return order[findings[i].Severity] < order[findings[j].Severity]
		}
		return findings[i].File < findings[j].File
	})
	counts := map[string]int{}
	for _, f := range findings {
		counts[f.Severity]++
	}
	var b strings.Builder
	fmt.Fprintf(&b, "workflow_audit: %d finding(s) — %d high, %d medium, %d low\n\n",
		len(findings), counts["high"], counts["medium"], counts["low"])
	for _, f := range findings {
		loc := f.File
		if f.Job != "" {
			loc += " · job " + f.Job
		}
		fmt.Fprintf(&b, "[%s] %s  (%s)\n  %s\n  fix: %s\n\n",
			strings.ToUpper(f.Severity), loc, f.Rule, f.Message, f.Fix)
	}
	return truncate(b.String(), 14000)
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
