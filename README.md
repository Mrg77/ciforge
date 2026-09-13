# ciforge

*Read this in [French](README.fr.md).*

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**An AI agent that hardens and optimises GitHub Actions — supply chain, permissions, minutes.**

A CI runner usually holds more rights than the engineer who wrote the pipeline: it
deploys, it reads secrets, it pushes images. And it executes third-party code on a
single `uses:` line. ciforge treats that asymmetry as what it is — a security
surface — and reports it with the fix attached.

Built **from scratch on the Anthropic Messages API** — no agent framework, so the
loop is fully visible. Every workflow write passes through **policy-as-code**;
triggering a pipeline is refused outright.

> The agent advises. It never gates your CI and it never runs it: a gate must return
> the same verdict on the same diff, and a model cannot promise that. The `audit`
> subcommand is deterministic and needs no API key — that is what belongs in a
> pipeline.

## What it finds

| Rule | Severity | Why it matters |
|---|---|---|
| `action-not-pinned` | high | A tag is a pointer. Whoever controls the action's repo can move it to different code, and your pipeline runs it — with no diff in yours. |
| `pr-target-checkout` | high | `pull_request_target` runs with your secrets. Combined with a checkout of the PR head, anyone who opens a pull request runs code with access to them. |
| `script-injection` | high | A PR title is user input. Interpolated into a `run:` block it becomes code. |
| `long-lived-credentials` | high | A cloud key sits in repository secrets until someone rotates it, which nobody does. |
| `permissions-write-all` | high | Every scope granted to every job. |
| `permissions-unset` | medium | The job token inherits the repo default — usually write access. |
| `no-concurrency` | low | Superseded runs finish anyway, and still bill minutes. |

First-party `actions/*` are reported at **low** severity rather than high: a lower
risk, not a null one. Burying a real finding under fifty notices is the same as
hiding it.

## Install

```sh
# Homebrew
brew install mrg77/tap/ciforge

# or the installer (Linux, macOS)
curl -fsSL https://raw.githubusercontent.com/Mrg77/ciforge/main/install.sh | sh

# or with Go
go install github.com/Mrg77/ciforge@latest
```

## Use

Deterministic, no API key — this is the one that belongs in CI:

```sh
ciforge audit                  # exits 1 on a high finding
ciforge audit --fail-on medium
ciforge pin                    # the SHA for every tag-pinned action
```

With the agent:

```sh
export ANTHROPIC_API_KEY=...
export GITHUB_TOKEN=...        # raises the API rate limit when resolving SHAs

ciforge "audit my workflows and pin every third-party action"
ciforge "reduce permissions to the minimum each job needs"
ciforge "migrate the deploy workflow from AWS keys to OIDC"
```

## The tools

| Tool | What it does |
|---|---|
| `workflow_list` | Triggers, permissions, jobs — read the ground first |
| `workflow_audit` | The deterministic findings above |
| `actionlint` | The reference linter, when installed |
| `pin_actions` | Resolves tags to commit SHAs via the GitHub API |
| `cost_estimate` | Wasted minutes, priced per month |
| `read_file` | Look before editing |
| `edit_file` / `write_file` | **Gated** — a workflow is the deploy path |

## The guard

A workflow file is the shortest path to production. An agent that can rewrite one
can grant itself permissions, add a step that exfiltrates a secret, or ship code —
and none of it looks alarming in a diff.

| Action | Deploy / release workflow | Other workflow | Elsewhere |
|---|---|---|---|
| write / edit | **denied** | confirm | confirm |
| trigger a run | **denied** | **denied** | **denied** |
| audit, pin, cost, read | allow | allow | allow |

The context is read **passively** from the target path — nothing is fetched, nothing
is triggered. An unclassifiable target **fails closed**: a mutating action is not
allowed through because the tool could not tell what it was touching. An empty policy
file falls back to the default rather than silently allowing everything.

Without a TTY (CI, a pipe) a `confirm` becomes a deny. An agent does not edit
workflows unattended.

## OIDC, done properly

When ciforge proposes replacing long-lived keys, it insists on the part people skip:
the role's trust policy must be restricted to the repository **and** the branch or
environment. A wildcard subject hands the role to every repository in the
organisation, which undoes the entire point of the migration.

## LLMOps

Every model turn, tool call and guard decision is appended to a JSONL audit log, with
tokens priced per run:

```sh
CIFORGE_AUDIT=off ciforge "..."        # no file; the summary still prints
CIFORGE_MAX_COST=0.50 ciforge "..."    # stop before exceeding 0.50 USD
CIFORGE_MODEL=claude-haiku-4-5 ciforge "..."
```

## Honesty

- When `actionlint` is missing or the GitHub API rate-limits, ciforge says the check
  **did not run**. Silence must never read as success.
- Cost figures are **estimates**, labelled as such. Real timings live in the Actions
  usage page.
- Not every optimisation is worth its maintenance, and the tool says so.

## Licence

MIT.
