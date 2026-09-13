package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// auditWith writes a workflow into a temp repo and audits it.
func auditWith(t *testing.T, yml string) string {
	t.Helper()
	dir := t.TempDir()
	wf := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wf, "test.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	SetProjectRoot(dir)
	out, err := AuditTool{}.Run(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}
	return out
}

func TestDetectsUnpinnedThirdPartyAction(t *testing.T) {
	out := auditWith(t, `
name: t
on: [push]
permissions:
  contents: read
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - uses: dorny/paths-filter@v3
`)
	if !strings.Contains(out, "action-not-pinned") || !strings.Contains(out, "HIGH") {
		t.Fatalf("a tag-pinned third-party action must be a high finding, got:\n%s", out)
	}
}

// A correctly pinned action must not be reported: a scanner that flags the fix
// teaches people to ignore it.
func TestSHAPinnedActionIsAccepted(t *testing.T) {
	out := auditWith(t, `
name: t
on: [push]
permissions:
  contents: read
concurrency:
  group: t
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - uses: dorny/paths-filter@0e4a8c6effa4802afeda77dc8d303f8176d7dfad # v3
`)
	if strings.Contains(out, "action-not-pinned") {
		t.Fatalf("a SHA-pinned action must not be reported, got:\n%s", out)
	}
}

// GitHub-maintained actions are a lower risk, not a null one — they must be
// reported at low severity so a real finding is not buried.
func TestFirstPartyActionIsLowSeverity(t *testing.T) {
	out := auditWith(t, `
name: t
on: [push]
permissions:
  contents: read
concurrency:
  group: t
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
`)
	if !strings.Contains(out, "action-not-pinned") {
		t.Fatal("a tag-pinned first-party action should still be reported")
	}
	if strings.Contains(out, "HIGH") && strings.Contains(out, "actions/checkout") {
		t.Fatalf("a GitHub-maintained action should not be high severity, got:\n%s", out)
	}
}

// The documented secret-exfiltration path: pull_request_target runs with repo
// secrets, and this checks out the PR's own code.
func TestDetectsPullRequestTargetCheckout(t *testing.T) {
	out := auditWith(t, `
name: t
on:
  pull_request_target:
permissions:
  contents: read
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@fbc6f3992d24b796d5a048ff273f7fcc4a7b6c09
        with:
          ref: ${{ github.event.pull_request.head.sha }}
`)
	if !strings.Contains(out, "pr-target-checkout") {
		t.Fatalf("pull_request_target with a PR checkout must be reported, got:\n%s", out)
	}
}

func TestDetectsScriptInjection(t *testing.T) {
	out := auditWith(t, `
name: t
on: [pull_request]
permissions:
  contents: read
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: echo "${{ github.event.pull_request.title }}"
`)
	if !strings.Contains(out, "script-injection") {
		t.Fatalf("event data interpolated into run: must be reported, got:\n%s", out)
	}
}

func TestDetectsMissingPermissions(t *testing.T) {
	out := auditWith(t, `
name: t
on: [push]
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
`)
	if !strings.Contains(out, "permissions-unset") {
		t.Fatalf("a workflow without top-level permissions must be reported, got:\n%s", out)
	}
}

func TestDetectsLongLivedCredentials(t *testing.T) {
	out := auditWith(t, `
name: t
on: [push]
permissions:
  contents: read
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - uses: aws-actions/configure-aws-credentials@a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0
        with:
          aws-access-key-id: ${{ secrets.AWS_ACCESS_KEY_ID }}
          aws-secret-access-key: ${{ secrets.AWS_SECRET_ACCESS_KEY }}
`)
	if !strings.Contains(out, "long-lived-credentials") {
		t.Fatalf("a long-lived cloud key must be reported, got:\n%s", out)
	}
	if !strings.Contains(out, "OIDC") {
		t.Fatal("the fix must point at OIDC")
	}
}

func TestCleanWorkflowHasNoFindings(t *testing.T) {
	out := auditWith(t, `
name: t
on: [push]
permissions:
  contents: read
concurrency:
  group: t
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@fbc6f3992d24b796d5a048ff273f7fcc4a7b6c09 # v5
      - run: echo hi
`)
	if !strings.Contains(out, "no findings") {
		t.Fatalf("a hardened workflow should produce no findings, got:\n%s", out)
	}
}

func TestConfineRejectsTraversal(t *testing.T) {
	SetProjectRoot(t.TempDir())
	if _, err := confine("../../etc/passwd"); err == nil {
		t.Fatal("a path escaping the project root must be rejected")
	}
}
