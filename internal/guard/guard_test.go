package guard

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Mrg77/ciforge/internal/agent"
	"github.com/Mrg77/ciforge/internal/tools"
)

type stubTool struct {
	name   string
	danger tools.Danger
}

func (s stubTool) Name() string                                         { return s.name }
func (s stubTool) Description() string                                  { return "" }
func (s stubTool) Schema() map[string]any                               { return nil }
func (s stubTool) Danger() tools.Danger                                 { return s.danger }
func (s stubTool) Run(context.Context, json.RawMessage) (string, error) { return "", nil }

func input(path string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"path": path})
	return b
}

func alwaysConfirm(_, _, _ string) bool { return true }
func neverConfirm(_, _, _ string) bool  { return false }

// TestDeployWorkflowWriteIsDenied is the headline behaviour: a workflow that
// reaches production is the shortest path to production, and the agent does not
// get to rewrite it. No approver can override a DENY.
func TestDeployWorkflowWriteIsDenied(t *testing.T) {
	g := New(Default(), alwaysConfirm)
	w := stubTool{name: "write_file", danger: tools.Mutating}

	if d, reason := g.Check(w, input(".github/workflows/deploy.yml")); d != agent.Deny {
		t.Fatalf("writing a deploy workflow must be DENIED, got %v (%s)", d, reason)
	}
	if d, _ := g.Check(w, input(".github/workflows/release.yml")); d != agent.Deny {
		t.Fatalf("writing a release workflow must be DENIED, got %v", d)
	}
}

// TestOrdinaryWorkflowWriteNeedsApproval — any workflow change alters what runs
// on every push, so it still needs a human.
func TestOrdinaryWorkflowWriteNeedsApproval(t *testing.T) {
	g := New(Default(), neverConfirm)
	w := stubTool{name: "edit_file", danger: tools.Mutating}

	if d, _ := g.Check(w, input(".github/workflows/ci.yml")); d == agent.Allow {
		t.Fatal("a workflow edit must not proceed without approval")
	}

	g = New(Default(), alwaysConfirm)
	if d, _ := g.Check(w, input(".github/workflows/ci.yml")); d != agent.Allow {
		t.Fatal("a CI workflow edit with approval should be allowed")
	}
}

// TestTriggerIsAlwaysDenied — an agent does not run your pipeline, ever.
func TestTriggerIsAlwaysDenied(t *testing.T) {
	g := New(Default(), alwaysConfirm)
	trig := stubTool{name: "workflow_dispatch", danger: tools.Destructive}

	if d, _ := g.Check(trig, input(".github/workflows/ci.yml")); d != agent.Deny {
		t.Fatalf("triggering a workflow must be DENIED regardless of approval, got %v", d)
	}
}

// TestReadOnlyAlwaysAllowed — auditing, pinning proposals and cost estimates
// change nothing, so a misconfigured policy must never block them.
func TestReadOnlyAlwaysAllowed(t *testing.T) {
	g := New(Default(), neverConfirm)
	for _, name := range []string{"workflow_audit", "pin_actions", "cost_estimate", "workflow_list", "read_file"} {
		ro := stubTool{name: name, danger: tools.ReadOnly}
		if d, _ := g.Check(ro, input(".github/workflows/deploy.yml")); d != agent.Allow {
			t.Fatalf("read-only tool %q must always be allowed, got %v", name, d)
		}
	}
}

// TestEmptyPolicyFallsBackToDefault — a blank policy file must not silently
// disable the guard.
func TestEmptyPolicyFallsBackToDefault(t *testing.T) {
	g := New(&Policy{Version: 1}, alwaysConfirm)
	w := stubTool{name: "write_file", danger: tools.Mutating}

	if d, _ := g.Check(w, input(".github/workflows/deploy.yml")); d != agent.Deny {
		t.Fatalf("an empty policy must fall back to the default, got %v", d)
	}
}

// TestUnknownContextFailsClosed — when the target cannot be classified, a
// mutating action must not slip through unguarded.
func TestUnknownContextFailsClosed(t *testing.T) {
	g := New(Default(), neverConfirm)
	w := stubTool{name: "write_file", danger: tools.Mutating}

	if d, _ := g.Check(w, input("")); d == agent.Allow {
		t.Fatal("an unknown context must not allow a mutating action without approval")
	}
}
