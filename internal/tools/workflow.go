package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// run executes a command with a timeout, returning combined output.
func run(ctx context.Context, dir string, timeout time.Duration, name string, args ...string) (string, error) {
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(c, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if c.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("%s timed out after %s", name, timeout)
	}
	return string(out), err
}

func have(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// workflowFiles lists .github/workflows/*.yml under the project root.
func workflowFiles(root string) ([]string, error) {
	dir := filepath.Join(root, ".github", "workflows")
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if ext := strings.ToLower(filepath.Ext(p)); ext == ".yml" || ext == ".yaml" {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// workflow is the subset of the schema these tools reason about.
type workflow struct {
	Name        string         `yaml:"name"`
	On          any            `yaml:"on"`
	Permissions any            `yaml:"permissions"`
	Jobs        map[string]job `yaml:"jobs"`
	Concurrency any            `yaml:"concurrency"`
}

type job struct {
	Name        string `yaml:"name"`
	RunsOn      any    `yaml:"runs-on"`
	Permissions any    `yaml:"permissions"`
	Steps       []step `yaml:"steps"`
	Environment any    `yaml:"environment"`
}

type step struct {
	Name string         `yaml:"name"`
	Uses string         `yaml:"uses"`
	Run  string         `yaml:"run"`
	With map[string]any `yaml:"with"`
	Env  map[string]any `yaml:"env"`
}

// parse reads and decodes a workflow file.
func parse(path string) (*workflow, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var w workflow
	if err := yaml.Unmarshal(b, &w); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return &w, nil
}

// triggers normalizes the `on:` key, which YAML may give us as a string, a list
// or a map.
func (w *workflow) triggers() []string {
	switch v := w.On.(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, t := range v {
			out = append(out, fmt.Sprint(t))
		}
		return out
	case map[string]any:
		out := make([]string, 0, len(v))
		for k := range v {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	return nil
}

// ---------------------------------------------------------------------------
// workflow_list
// ---------------------------------------------------------------------------

// ListWorkflowsTool gives the agent the lay of the land before it proposes
// anything: which workflows exist, what triggers them, what they may do.
type ListWorkflowsTool struct{}

func (ListWorkflowsTool) Name() string   { return "workflow_list" }
func (ListWorkflowsTool) Danger() Danger { return ReadOnly }
func (ListWorkflowsTool) Description() string {
	return "List the repository's GitHub Actions workflows with their triggers, top-level " +
		"permissions, jobs and step count. Start here: never propose a change without reading " +
		"what already runs."
}
func (ListWorkflowsTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
}

func (ListWorkflowsTool) Run(_ context.Context, _ json.RawMessage) (string, error) {
	root := projectRoot
	if root == "" {
		root, _ = filepath.Abs(".")
	}
	files, err := workflowFiles(root)
	if err != nil || len(files) == 0 {
		return "no workflows found under .github/workflows/", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d workflow(s):\n\n", len(files))
	for _, f := range files {
		rel, _ := filepath.Rel(root, f)
		w, err := parse(f)
		if err != nil {
			fmt.Fprintf(&b, "- %s — UNPARSEABLE: %v\n", rel, err)
			continue
		}
		perms := "unset (defaults to the repo setting — usually write)"
		if w.Permissions != nil {
			perms = fmt.Sprint(w.Permissions)
		}
		fmt.Fprintf(&b, "- %s\n  name: %s\n  on: %s\n  permissions: %s\n  jobs: %d\n",
			rel, w.Name, strings.Join(w.triggers(), ", "), perms, len(w.Jobs))
	}
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// actionlint
// ---------------------------------------------------------------------------

// ActionlintTool runs the reference linter when it is installed. Deterministic
// checks come first; the agent adds what a linter cannot see.
type ActionlintTool struct{}

func (ActionlintTool) Name() string   { return "actionlint" }
func (ActionlintTool) Danger() Danger { return ReadOnly }
func (ActionlintTool) Description() string {
	return "Run actionlint, the reference GitHub Actions linter (syntax, expressions, shell). " +
		"Reports plainly when it is not installed rather than implying the check passed."
}
func (ActionlintTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
}

func (ActionlintTool) Run(ctx context.Context, _ json.RawMessage) (string, error) {
	if !have("actionlint") {
		return "actionlint is not installed — this check did NOT run. Install it " +
			"(https://github.com/rhysd/actionlint) for syntax and expression validation.", nil
	}
	out, err := run(ctx, projectRoot, 2*time.Minute, "actionlint", "-color=never")
	if err != nil {
		return truncate(out, 8000), nil // findings, not an execution failure
	}
	return "actionlint: no issues.", nil
}
