package guard

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/Mrg77/ciforge/internal/agent"
	"github.com/Mrg77/ciforge/internal/tools"
)

// Guard adapts a policy-as-code Policy to the agent's Guard interface. It maps a
// tool call to an (action, context) pair, evaluates the policy, and translates
// the policy Action into an agent Decision.
//
// Read-only tools skip the policy entirely (they can't change anything), so a
// misconfigured policy can never block the agent from merely *looking*.
type Guard struct {
	policy  *Policy
	confirm ConfirmFunc
}

// ConfirmFunc asks the human to approve an action, returning true to proceed.
// It lets the caller decide the UX (a TTY prompt, auto-deny in CI, etc.).
type ConfirmFunc func(action, context, message string) bool

// New builds a Guard. If confirm is nil, "confirm" decisions are treated as
// deny — the fail-safe choice (never auto-approve a destructive action). An
// empty policy (nil, or zero rules) falls back to Default() so a blank policy
// file can never silently disable the guard (allow-all).
func New(p *Policy, confirm ConfirmFunc) *Guard {
	if p == nil || len(p.Rules) == 0 {
		p = Default()
	}
	return &Guard{policy: p, confirm: confirm}
}

// Check implements agent.Guard. It is the single chokepoint every tool call
// passes through.
func (g *Guard) Check(t tools.Tool, input json.RawMessage) (agent.Decision, string) {
	// Read-only actions are always allowed — nothing to gate.
	if t.Danger() == tools.ReadOnly {
		return agent.Allow, "read-only action"
	}

	action := actionString(t, input)
	ctx := detectContext(input)

	d := g.policy.Evaluate(action, ctx)

	// Fail CLOSED for destructive actions in an UNKNOWN context. If the tool is
	// Destructive and we couldn't positively classify the context as a safe one
	// (dev/staging), we refuse to guess — UNLESS a policy rule explicitly denies
	// or explicitly allows it (an operator's intentional decision wins). A mere
	// "confirm" is NOT enough to prove non-prod, so we upgrade it to deny here:
	// a destroy on infra we can't prove is non-prod must not run on a yes/N slip.
	// This closes the "prod isn't literally named 'prod'" gap.
	// In a CI repository the risky act is MUTATING, not "destructive": writing a
	// workflow file is what can grant permissions or ship code. So both classes
	// fail closed when the context cannot be positively classified as safe.
	if t.Danger() != tools.ReadOnly && !isKnownSafe(ctx) {
		explicit := d.Rule != "" && (d.Action == ActionDeny || d.Action == ActionAllow)
		if !explicit {
			return agent.Deny, "blocked: could not confirm this is a non-production context (" + describeCtx(ctx) + "). Name the target explicitly, or add an allow/deny rule for it in the policy."
		}
	}

	switch d.Action {
	case ActionDeny:
		return agent.Deny, reason(d, ctx)
	case ActionConfirm:
		if g.confirm == nil || !g.confirm(action, ctx, d.Message) {
			// No approver, or the human said no → treat as deny (fail-safe).
			return agent.Deny, "not approved: " + reason(d, ctx)
		}
		return agent.Allow, "approved by user: " + reason(d, ctx)
	case ActionWarn:
		// A warn on a MUTATING/DESTRUCTIVE action is not a free pass for an
		// autonomous agent — route it through the approver like a confirm.
		// Only read-only warns run without a gate.
		if t.Danger() != tools.ReadOnly {
			if g.confirm == nil || !g.confirm(action, ctx, d.Message) {
				return agent.Deny, "not approved (warned): " + reason(d, ctx)
			}
			return agent.Allow, "approved after warning: " + reason(d, ctx)
		}
		return agent.Allow, "warning: " + reason(d, ctx)
	default:
		return agent.Allow, "allowed by policy"
	}
}

// isKnownSafe reports whether the context was positively classified as a
// non-production environment we're comfortable letting a destroy through
// (subject to the policy's own confirm rule). "prod" and "unknown" are NOT safe.
func isKnownSafe(ctx string) bool {
	return ctx == "dev" || ctx == "staging"
}

func describeCtx(ctx string) string {
	if ctx == "" {
		return "context: unknown"
	}
	return "context: " + ctx
}

func reason(d Decision, ctx string) string {
	msg := d.Message
	if d.Rule != "" {
		msg = "[" + d.Rule + "] " + msg
	}
	if ctx != "" {
		msg += " (context: " + ctx + ")"
	}
	return msg
}

// actionString maps a tool to the action text the policy matches against. We use
// the tool name (e.g. "terraform_destroy" → "destroy"), keeping the policy YAML
// readable ("destroy", "apply") rather than tied to internal tool names.
func actionString(t tools.Tool, input json.RawMessage) string {
	name := t.Name()
	switch {
	// Running a playbook for real against an inventory is the destructive act in
	// Ansible: it changes remote machines. There is no "destroy" verb, which is
	// exactly why an explicit gate matters — `ansible-playbook` looks harmless.
	// What matters is not the tool's name but what it writes. A workflow file is
	// how an agent could grant itself rights, exfiltrate a secret, or ship code
	// straight to production — the workflow IS the deploy path.
	case strings.Contains(name, "trigger"), strings.Contains(name, "dispatch"):
		return "workflow dispatch"
	case strings.Contains(name, "write"), strings.Contains(name, "edit"):
		if targetsWorkflow(input) {
			return "workflow write"
		}
		return "file write"
	default:
		return name
	}
}

// detectContext derives a context string the policy matches against. For a CI
// pipeline, "production" is not an inventory or a workspace — it is a workflow
// that can reach production: one that deploys, that runs on the default branch,
// or that binds a GitHub environment.
//
// Read passively from the tool input: nothing is fetched, nothing is triggered.
func detectContext(input json.RawMessage) string {
	var in struct {
		Path        string `json:"path"`
		Workflow    string `json:"workflow"`
		Environment string `json:"environment"`
	}
	_ = json.Unmarshal(input, &in)

	joined := strings.ToLower(strings.Join([]string{in.Environment, in.Workflow, in.Path}, " "))
	switch {
	case containsAny(joined, "prod", "production", "release", "deploy", "publish"):
		return "prod"
	case containsAny(joined, "staging", "preprod", "uat"):
		return "staging"
	case containsAny(joined, "ci", "test", "lint", "pr", "pull"):
		return "dev"
	}
	return strings.TrimSpace(joined)
}

// targetsWorkflow reports whether the tool input points at a GitHub Actions
// workflow file. Editing one is categorically different from editing a README.
func targetsWorkflow(input json.RawMessage) bool {
	var in struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(input, &in)
	p := strings.ToLower(filepath.ToSlash(in.Path))
	return strings.Contains(p, ".github/workflows/")
}

// containsAny reports whether s contains any of the given substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// containsWord reports whether s contains any of the given markers as a
// substring. Cheap and good enough for env-name detection.
func containsWord(s string, markers ...string) bool {
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}
