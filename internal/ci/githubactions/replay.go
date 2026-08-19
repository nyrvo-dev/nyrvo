package githubactions

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ReplayStep is one thing a job does.
type ReplayStep struct {
	// Name is the step name, or a description when the file gives none.
	Name string `json:"name"`
	// Command is the shell body, verbatim, for a run step; empty otherwise.
	Command string `json:"command"`
	// WorkingDirectory is the directory the step runs in, when declared.
	WorkingDirectory string `json:"working_directory,omitempty"`
	// Env lists "NAME=value" pairs as written in the file, sorted. Job-level
	// env applies to every step; step-level env overrides it for that step. A
	// value that references a secret is replaced with "<secret>".
	Env []string `json:"env,omitempty"`
	// Action is the uses reference for an action step; empty for a run step.
	Action string `json:"action,omitempty"`
	// Reason is non-empty when this step cannot be reproduced as written, and
	// says why in one sentence a user can act on.
	Reason string `json:"reason,omitempty"`
}

// ReplayPlan is the ordered list of things a job does, plus what must already
// be true before any of them can run.
type ReplayPlan struct {
	// Job is the job's id, the stable identifier a user passes on the command
	// line.
	Job string `json:"job"`
	// Steps are the job's steps in declaration order. A step is never dropped.
	Steps []ReplayStep `json:"steps"`
	// Prerequisites are things that must already be true before any step runs:
	// service containers, a job container image.
	Prerequisites []string `json:"prerequisites,omitempty"`
	// Notes carries what Nyrvo could not model, copied from the job.
	Notes []string `json:"notes,omitempty"`
}

// Replay turns a job into a plan of what it does, in order. It never executes
// anything: a step is printed, and a step Nyrvo cannot reproduce is still
// printed, marked with a reason. It is deterministic: two calls on the same
// job return identical plans, env order included.
func Replay(job *Job) ReplayPlan {
	if job == nil {
		return ReplayPlan{}
	}
	plan := ReplayPlan{
		Job: job.ID,
		// Steps is initialized so a job without steps serializes as [] rather
		// than null: the frozen machine contract reads "no steps", not "unknown".
		Steps: make([]ReplayStep, 0, len(job.Steps)),
		Notes: append([]string(nil), job.Notes...),
	}
	if plan.Job == "" {
		plan.Job = job.Name
	}
	for _, svc := range job.Services {
		plan.Prerequisites = append(plan.Prerequisites, servicePrerequisite(svc))
	}
	if job.Container != "" {
		plan.Prerequisites = append(plan.Prerequisites,
			fmt.Sprintf("steps run inside container %s, not on the host", job.Container))
	}
	// A matrix job is not one job. Printing its steps without saying so would
	// describe a single run as if it were the whole job, and the combination a
	// reader happens to reproduce is rarely the one that failed.
	if len(job.Matrix) > 0 {
		plan.Prerequisites = append(plan.Prerequisites,
			"this job runs once per matrix combination ("+matrixSummary(job.Matrix)+"); the steps below describe one of them")
	}
	for _, step := range job.Steps {
		plan.Steps = append(plan.Steps, replayStep(job, step))
	}
	return plan
}

// replayStep converts one step. A step is never dropped: when it cannot be
// reproduced as written, the reason says why and the command stays verbatim.
func replayStep(job *Job, step Step) ReplayStep {
	rs := ReplayStep{
		Name:             stepDescription(step),
		Command:          step.Run,
		WorkingDirectory: step.WorkingDirectory,
		Env:              mergedEnv(job.Env, step.Env),
		Action:           step.Uses,
	}
	switch {
	case step.Uses != "":
		rs.Reason = usesReason(step)
	case strings.Contains(step.Run, "${{"):
		rs.Reason = runExpressionReason(step.Run)
	}
	return rs
}

// stepDescription returns the step's name, or a description when the file
// gives none: the uses reference for an action step, the first line of the
// command for a run step.
func stepDescription(step Step) string {
	if step.Name != "" {
		return step.Name
	}
	switch {
	case step.Uses != "":
		return "uses " + step.Uses
	case step.Run != "":
		for _, line := range strings.Split(step.Run, "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

// mergedEnv overlays job-level env with step-level env and formats each pair
// as "NAME=value". Values stay the literal file text; a value that references
// a secret is replaced with "<secret>" so nothing that reads like a resolved
// secret is ever printed. The result is sorted by name.
func mergedEnv(jobEnv, stepEnv map[string]string) []string {
	merged := make(map[string]string, len(jobEnv)+len(stepEnv))
	for k, v := range jobEnv {
		merged[k] = v
	}
	for k, v := range stepEnv {
		merged[k] = v
	}
	names := make([]string, 0, len(merged))
	for k := range merged {
		names = append(names, k)
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)

	out := make([]string, 0, len(names))
	for _, k := range names {
		out = append(out, k+"="+envValue(merged[k]))
	}
	return out
}

// secretRef matches a reference to a secret in any spacing or casing a workflow
// may use: ${{ secrets.TOKEN }} and ${{secrets.TOKEN}} are the same reference,
// and matching only the first spelling would print the second one through.
var secretRef = regexp.MustCompile(`(?i)\$\{\{\s*secrets\s*\.`)

// envValue redacts anything that could be a resolved secret. The workflow file
// only ever holds the expression, but the plan is meant to be read and pasted,
// so a value that names a secret is replaced outright.
func envValue(v string) string {
	if secretRef.MatchString(v) {
		return "<secret>"
	}
	return v
}

// usesReason explains why a uses step cannot be reproduced: Nyrvo does not
// fetch or run actions. For the setup actions the project already understands
// it also states which version the step asks for, so a user can install it
// themselves.
func usesReason(step Step) string {
	if act := matchSetupAction(step.Uses); act != nil {
		return setupReason(act, step.Uses, step)
	}
	// Checking out the repository is the one action a reader has already
	// performed: they are standing in the checkout. Telling them to run it
	// themselves would be the first line of the plan and the wrong advice.
	if ref, _, _ := strings.Cut(step.Uses, "@"); strings.EqualFold(strings.TrimSpace(ref), "actions/checkout") {
		return fmt.Sprintf("uses %s; Nyrvo does not run actions, but your working tree is already the checkout, so there is nothing to do", step.Uses)
	}
	return fmt.Sprintf("uses %s; Nyrvo does not fetch or run actions, so run it yourself", step.Uses)
}

// setupReason names the action and the version input it asks for. A version
// read from a file is resolved by the action at run time, so it is named but
// not claimed as a version.
func setupReason(act *setupAction, uses string, step Step) string {
	if raw := step.With[act.file]; raw != "" {
		return fmt.Sprintf("%s installs %s from %s, resolved at run time; %s",
			uses, act.runtime, raw, installHint(act.runtime))
	}
	raw := step.With[act.version]
	if raw == "" {
		return fmt.Sprintf("%s installs %s but declares no %s; %s",
			uses, act.runtime, act.version, installHint(act.runtime))
	}
	// A version that is itself an expression names nothing a reader can
	// install: the runner decides it, usually from the matrix.
	if strings.Contains(raw, "${{") {
		return fmt.Sprintf("%s installs the %s named by %s, which the runner decides; %s",
			uses, act.runtime, raw, installHint(act.runtime))
	}
	return fmt.Sprintf("%s installs %s %s; %s",
		uses, act.runtime, raw, installHint(act.runtime))
}

// setupInstallHint suggests how a runtime is usually installed, so a reason
// reads as advice a user can act on. Runtimes without a widely-known manager
// fall back to the default branch.
var setupInstallHint = map[string]string{
	"node":   "install it yourself or use nvm",
	"python": "install it yourself or use pyenv",
}

func installHint(runtime string) string {
	if hint, ok := setupInstallHint[runtime]; ok {
		return hint
	}
	return "install it yourself"
}

// runExpressionReason names the first expression a run command contains: its
// value is decided by the runner, so the step cannot be reproduced as written.
func runExpressionReason(command string) string {
	expr := firstExpression(command)
	if expr == "" {
		return "command contains a ${{ ... }} expression, whose value is unknown outside the runner"
	}
	return fmt.Sprintf("command contains %s, whose value is unknown outside the runner", expr)
}

// firstExpression returns the first ${{ ... }} expression in s, or "".
func firstExpression(s string) string {
	i := strings.Index(s, "${{")
	if i < 0 {
		return ""
	}
	j := strings.Index(s[i:], "}}")
	if j < 0 {
		return s[i:]
	}
	return s[i : i+j+2]
}

// matrixSummary renders the matrix as "key=[a b], key=[c d]", with keys sorted
// so the same job always produces the same line.
func matrixSummary(matrix map[string][]string) string {
	keys := make([]string, 0, len(matrix))
	for k := range matrix {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=[%s]", k, strings.Join(matrix[k], " ")))
	}
	return strings.Join(parts, ", ")
}

// servicePrerequisite is one line a user can act on: the image must be running
// and reachable by the service id, the hostname the job reaches it by.
func servicePrerequisite(svc Service) string {
	if svc.Image == "" {
		return fmt.Sprintf("service %q must be running and reachable", svc.ID)
	}
	return fmt.Sprintf("%s must be reachable as %q", svc.Image, svc.ID)
}
