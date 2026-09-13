// Package guard is tfforge's policy-as-code safety layer — the same idea that
// powers opsforge's shell guards, applied to an AI agent's actions instead of a
// human's keystrokes. The pitch: the same declarative policy that guards you at
// the keyboard guards your agent.
//
// A policy is an ordered list of rules; each rule matches an action (regex) and
// a context (regex) and yields an action: allow / warn / confirm / deny. First
// match wins. Rules live in YAML so they're versionable and reviewable in a PR —
// the whole point of policy-as-code: the safety logic isn't hard-coded in the
// agent, it's data you can audit and change.
//
// This mirrors opsforge/internal/shellcfg's engine deliberately, so a DevOps who
// knows one knows the other. It is standalone (no import of opsforge) so tfforge
// stays a single self-contained binary.
package guard

import (
	"fmt"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Action is what a matching rule decides. Same vocabulary as opsforge.
type Action string

const (
	ActionAllow   Action = "allow"   // run it, no interruption
	ActionWarn    Action = "warn"    // run it, but surface the message
	ActionConfirm Action = "confirm" // require explicit human approval first
	ActionDeny    Action = "deny"    // block it outright
)

var validActions = map[Action]bool{
	ActionAllow: true, ActionWarn: true, ActionConfirm: true, ActionDeny: true,
}

// Match is a rule's predicate. An empty field matches anything. Both are RE2
// regexes; a plain string like "terraform destroy" behaves like a substring
// match, so simple rules stay simple.
type Match struct {
	Action  string `yaml:"action"`  // matches the agent action, e.g. "terraform destroy"
	Context string `yaml:"context"` // matches the detected context, e.g. "prod"
}

// Rule is one declarative guard: when Action matches the tool action AND Context
// matches the detected context, apply Do (showing Message).
type Rule struct {
	Name    string `yaml:"name"`
	Match   Match  `yaml:"match"`
	Do      Action `yaml:"do"`
	Message string `yaml:"message"`

	actionRe *regexp.Regexp
	ctxRe    *regexp.Regexp
}

// Policy is the top-level document: an ordered rule list. Order matters — first
// match wins.
type Policy struct {
	Version int    `yaml:"version"`
	Rules   []Rule `yaml:"rules"`
}

// Decision is the outcome of evaluating an action in a context.
type Decision struct {
	Action  Action
	Message string
	Rule    string // matching rule name, empty if none matched
}

func compilePattern(pat string) (*regexp.Regexp, error) {
	if pat == "" {
		return nil, nil // nil = match anything
	}
	return regexp.Compile("(?i)" + pat) // case-insensitive, like opsforge
}

func (r *Rule) compile() error {
	if r.Do == "" {
		r.Do = ActionConfirm // sensible default: ask, don't block
	}
	if !validActions[r.Do] {
		return fmt.Errorf("rule %q: invalid action %q (want allow|warn|confirm|deny)", r.Name, r.Do)
	}
	var err error
	if r.actionRe, err = compilePattern(r.Match.Action); err != nil {
		return fmt.Errorf("rule %q: bad action pattern: %w", r.Name, err)
	}
	if r.ctxRe, err = compilePattern(r.Match.Context); err != nil {
		return fmt.Errorf("rule %q: bad context pattern: %w", r.Name, err)
	}
	return nil
}

func (r *Rule) matches(action, context string) bool {
	if r.actionRe != nil && !r.actionRe.MatchString(action) {
		return false
	}
	if r.ctxRe != nil && !r.ctxRe.MatchString(context) {
		return false
	}
	return true
}

// Evaluate returns the decision for an action in a context: the first matching
// rule wins; if none match, the action is allowed.
func (p *Policy) Evaluate(action, context string) Decision {
	for i := range p.Rules {
		if p.Rules[i].matches(action, context) {
			return Decision{Action: p.Rules[i].Do, Message: p.Rules[i].Message, Rule: p.Rules[i].Name}
		}
	}
	return Decision{Action: ActionAllow}
}

// Validate compiles every rule; call once after loading a policy.
func (p *Policy) Validate() error {
	for i := range p.Rules {
		if err := p.Rules[i].compile(); err != nil {
			return err
		}
	}
	return nil
}

// Parse reads a YAML policy and compiles it.
func Parse(data []byte) (*Policy, error) {
	var p Policy
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parsing policy: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Default is the built-in policy: fail-safe.
//
// A workflow file is the shortest path to production. An agent that can rewrite
// one can grant itself permissions, add a step that exfiltrates a secret, or ship
// code — and none of it looks alarming in a diff. So: writing a deployment or
// release workflow is denied, any other workflow write needs approval, and
// triggering a workflow is never the agent's call.
func Default() *Policy {
	p := &Policy{
		Version: 1,
		Rules: []Rule{
			{
				Name:    "deny writing a deployment or release workflow",
				Match:   Match{Action: `workflow write`, Context: `prod|production|release|deploy`},
				Do:      ActionDeny,
				Message: "editing a workflow that reaches production is blocked. This is policy-as-code — edit the policy to change it, do not work around it.",
			},
			{
				Name:    "deny triggering a workflow",
				Match:   Match{Action: `workflow dispatch`},
				Do:      ActionDeny,
				Message: "an agent does not get to run your pipeline. Trigger it yourself once you have read the diff.",
			},
			{
				Name:    "confirm any workflow write",
				Match:   Match{Action: `workflow write`},
				Do:      ActionConfirm,
				Message: "a workflow change alters what runs on every push — review it before it lands.",
			},
			// Everything else — parsing, auditing, pinning proposals, cost
			// estimates — is read-only and flows freely.
		},
	}
	_ = p.Validate() // the built-in is always valid
	return p
}
