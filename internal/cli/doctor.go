package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/nyrvo-dev/nyrvo/internal/agent"
	"github.com/nyrvo-dev/nyrvo/internal/analysis"
	"github.com/nyrvo-dev/nyrvo/internal/capture"
	"github.com/nyrvo-dev/nyrvo/internal/ci/githubactions"
	"github.com/nyrvo-dev/nyrvo/internal/diagnostic"
	"github.com/nyrvo-dev/nyrvo/internal/diff"
	"github.com/nyrvo-dev/nyrvo/internal/finding"
	"github.com/nyrvo-dev/nyrvo/internal/output"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// doctor defaults to the pair the tool exists to compare, so the common case is
// one word: `nyrvo doctor`.
const (
	defaultDoctorA = "local"
	defaultDoctorB = ciSnapshotName
)

func runDoctor(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "write the diagnosis as JSON")
	// --ai is opt-in and does not become a default: `nyrvo doctor` must never
	// hand a user's environment to anything, and a flag is the smallest thing
	// that makes the choice visible in the command the user typed.
	withAI := fs.Bool("ai", false, "also print an analysis request to hand to your own AI agent")
	// Like --fail-on, this takes a value and so must be written --agent=claude:
	// splitFlags separates flags from operands assuming every flag is boolean,
	// and the separate form cannot be told apart from a snapshot named "claude".
	agentName := fs.String("agent", "", "run the analysis request through an installed agent CLI ("+strings.Join(agent.Names(), ", ")+")")
	// failOn is the only flag in Nyrvo that takes a value, so it must be
	// written --fail-on=high: splitFlags separates flags from operands on the
	// assumption that every flag is boolean. Writing them apart fails loudly
	// here rather than silently treating the value as a snapshot name.
	failOn := fs.String("fail-on", "", "exit non-zero when a finding of this severity or worse exists (high, medium, low)")
	flags, operands := splitFlags(args)
	if err := fs.Parse(flags); err != nil {
		if strings.Contains(err.Error(), "needs an argument") {
			return usageErr("doctor: %v (write it as --flag=value)", err)
		}
		return usageErr("doctor: %v", err)
	}
	threshold, err := parseFailOn(*failOn)
	if err != nil {
		return err
	}
	selected, err := selectAgent(*agentName)
	if err != nil {
		return err
	}
	// Naming an agent is asking for it to run, so it opts into the AI layer on
	// its own. Requiring --ai alongside it would only be ceremony: the choice is
	// already visible in the command the user typed.
	useAI := *withAI || selected != nil

	a, b := defaultDoctorA, defaultDoctorB
	var snapA, snapB *snapshot.Snapshot

	switch len(operands) {
	case 0:
		store := snapshot.NewStore("")
		if snapA, err = loadForDoctor(store, a); err != nil {
			return err
		}
		if snapB, err = loadForDoctor(store, b); err != nil {
			return err
		}
	case 1:
		// One operand is a run reference, never a snapshot name: comparing a
		// single snapshot against nothing is meaningless, so there is no
		// ambiguity to resolve. Something that is not a run reference is a
		// mistyped command, and is answered with usage rather than with a
		// failed fetch.
		if !githubactions.IsRunReference(operands[0]) {
			return usageErr("%q is not a run id or run URL; pass two snapshot names to compare captures, or nothing to diagnose %s against %s",
				operands[0], defaultDoctorA, defaultDoctorB)
		}
		if snapA, snapB, err = diagnoseRun(ctx, operands[0], nil, stderr); err != nil {
			return err
		}
	case 2:
		// A run reference here means the user tried to name a run's job. That
		// form is not supported, and saying so beats reporting the run URL as
		// an invalid snapshot name.
		if githubactions.IsRunReference(operands[0]) {
			return usageErr("to diagnose a specific job of a run, import it first:\n  nyrvo ci import %s %q\n  nyrvo doctor", operands[0], operands[1])
		}
		a, b = operands[0], operands[1]
		store := snapshot.NewStore("")
		if snapA, err = loadForDoctor(store, a); err != nil {
			return err
		}
		if snapB, err = loadForDoctor(store, b); err != nil {
			return err
		}
	default:
		// Two operands are always two snapshot names. A run whose job needs
		// naming goes through `nyrvo ci import <run> <job>` first, which keeps
		// this command free of a "is that a snapshot or a run?" guess.
		return usageErr("doctor takes a run id or run URL, two snapshot names, or nothing to diagnose %s against %s", defaultDoctorA, defaultDoctorB)
	}

	// The diff is not printed here: doctor reports conclusions, and a user who
	// wants the underlying evidence runs `nyrvo diff`. Keeping the two commands
	// separate is what lets someone who disagrees with a rule still trust the
	// comparison beneath it. It is still needed as the evidence an agent reads
	// under --ai, which is why it is kept rather than discarded.
	differences, findings := diagnostic.Analyze(snapA, snapB)

	// A snapshot's own notes — the run's conclusion, the step that failed, what
	// Nyrvo could not model — answer "why did this fail?" in a way no
	// environment rule can. Without them a diagnosis of a failed run would list
	// two low-severity platform differences and never mention the failure.
	context := evidenceContext(snapA, snapB)

	if err := writeDiagnosis(ctx, stdout, stderr, a, b, snapA, snapB, differences, findings, context, *asJSON, useAI, selected); err != nil {
		return err
	}
	return failOnThreshold(stderr, findings, threshold)
}

// selectAgent resolves --agent. An unknown name is a usage error naming the
// alternatives, and an agent that is not installed is reported before any
// evidence is gathered: the user's next step is to install it or pick another,
// and neither is helped by a diagnosis appearing first.
func selectAgent(name string) (*agent.Agent, error) {
	if name == "" {
		return nil, nil
	}
	selected, ok := agent.Lookup(name)
	if !ok {
		return nil, usageErr("--agent=%s is not an agent Nyrvo knows; use one of %s", name, strings.Join(agent.Names(), ", "))
	}
	if !selected.Available() {
		return nil, fmt.Errorf("%s is not installed; install it, choose another agent (%s), or run --ai alone to print the request and paste it yourself",
			name, strings.Join(agent.Names(), ", "))
	}
	return &selected, nil
}

// writeDiagnosis renders the report the user asked for.
//
// Under --json the analysis request replaces the diagnosis document rather than
// following it: two JSON documents on one stream is not something a consumer can
// pipe into a parser, and the request already carries the findings the
// diagnosis document would have repeated.
func writeDiagnosis(
	ctx context.Context,
	stdout, stderr io.Writer,
	a, b string,
	snapA, snapB *snapshot.Snapshot,
	differences *diff.Result,
	findings []finding.Finding,
	notes []string,
	asJSON, withAI bool,
	selected *agent.Agent,
) error {
	if withAI {
		in := analysis.Build(snapA, snapB, differences, findings, notes)
		prompt := analysis.Prompt(in)
		// The deterministic report is printed first and in full. What Nyrvo
		// established on its own is the part that does not depend on a model,
		// and it should be readable by someone who never pastes the request.
		if !asJSON {
			if err := output.DoctorText(stdout, a, b, findings, notes...); err != nil {
				return err
			}
		}
		if selected == nil {
			if asJSON {
				return output.AIRequestJSON(stdout, in, prompt)
			}
			return output.AIRequestText(stdout, in, prompt)
		}
		return runAgent(ctx, stdout, stderr, in, prompt, *selected, asJSON)
	}
	if asJSON {
		return output.DoctorJSON(stdout, a, b, findings, notes...)
	}
	return output.DoctorText(stdout, a, b, findings, notes...)
}

// runAgent discloses what is about to happen, runs the agent, and prints its
// answer under a heading of its own.
//
// The disclosure goes to stderr under --json for the same reason progress
// always does: stdout must stay a single document a consumer can parse. It is
// still written before the agent runs, which is the part that matters — a
// disclosure a user reads after the request has already left is not one.
func runAgent(ctx context.Context, stdout, stderr io.Writer, in analysis.Input, prompt string, selected agent.Agent, asJSON bool) error {
	disclosureTo := stdout
	if asJSON {
		disclosureTo = stderr
	}
	command := selected.Command(prompt)
	if err := output.AIAgentDisclosure(disclosureTo, selected.Name(), command); err != nil {
		return err
	}

	result, err := selected.Analyze(ctx, prompt)
	if err != nil {
		if errors.Is(err, agent.ErrUnavailable) {
			return fmt.Errorf("%w; run --ai alone to print the request and paste it yourself", err)
		}
		return err
	}

	if asJSON {
		return output.AIResultJSON(stdout, in, prompt, selected.Name(), command, result)
	}
	return output.AIAnalysisText(stdout, selected.Name(), result)
}

// severityRank orders severities for the --fail-on threshold. It is separate
// from the renderer's ordering on purpose: this one has to answer "is this at
// least as serious as X?", which is a comparison, not a sort key.
var severityRank = map[finding.Severity]int{
	finding.SeverityLow:    1,
	finding.SeverityMedium: 2,
	finding.SeverityHigh:   3,
}

// parseFailOn turns the flag value into a threshold. An empty value means the
// exit code never depends on findings, which stays the default: a diagnosis is
// an answer, and a CI job should opt in before a low-severity note can fail it.
func parseFailOn(v string) (int, error) {
	if v == "" {
		return 0, nil
	}
	rank, ok := severityRank[finding.Severity(strings.ToLower(strings.TrimSpace(v)))]
	if !ok {
		return 0, usageErr("--fail-on=%s is not a severity; use high, medium or low", v)
	}
	return rank, nil
}

// failOnThreshold reports the opted-in failure, after the report has already
// been written: the diagnosis is the point, and the exit code is a signal for
// whatever ran the command.
func failOnThreshold(stderr io.Writer, findings []finding.Finding, threshold int) error {
	if threshold == 0 {
		return nil
	}
	worst, count := 0, 0
	for _, f := range findings {
		if rank := severityRank[f.Severity]; rank >= threshold {
			count++
			if rank > worst {
				worst = rank
			}
		}
	}
	if count == 0 {
		return nil
	}
	name := map[int]string{1: "low", 2: "medium", 3: "high"}[worst]
	_, err := fmt.Fprintf(stderr, "nyrvo: %d finding(s) at or above the --fail-on threshold; worst severity %s\n", count, name)
	if err != nil {
		return err
	}
	// errSilent exits non-zero without printing again: the reason is already on
	// stderr in the caller's own words.
	return errSilent
}

// errSilent asks the CLI for a non-zero exit with no further message.
var errSilent = errors.New("")

// evidenceContext collects what the snapshots reported about themselves.
//
// Only notes are collected, never values: a snapshot's notes are written by
// Nyrvo's own converters, which are forbidden from putting environment values
// in them.
func evidenceContext(snaps ...*snapshot.Snapshot) []string {
	var out []string
	for _, s := range snaps {
		if s == nil || s.Source == nil {
			continue
		}
		out = append(out, s.Source.Notes...)
	}
	return out
}

// diagnoseRun answers "why did this run fail?" in one command: it imports the
// run and captures this machine, both in memory.
//
// Nothing is saved. A one-shot question should not overwrite snapshots the user
// captured deliberately — `nyrvo capture` and `nyrvo ci import` exist for that —
// and capturing fresh means the machine side describes the machine as it is
// now, not as it was whenever someone last ran capture.
//
// Progress goes to stderr so a --json diagnosis stays a clean document on
// stdout.
func diagnoseRun(ctx context.Context, ref string, jobOperands []string, stderr io.Writer) (local, ci *snapshot.Snapshot, err error) {
	imported, err := importRun(ctx, ref, jobOperands)
	if err != nil {
		return nil, nil, err
	}
	if _, err := fmt.Fprintf(stderr, "Diagnosing %s job %q (%s) against this machine.\n", imported.Ref, imported.Job, imported.Why); err != nil {
		return nil, nil, err
	}
	if imported.LogNote != "" {
		if _, err := fmt.Fprintf(stderr, "Warning: %s\n", imported.LogNote); err != nil {
			return nil, nil, err
		}
	}

	res, err := capture.Run(ctx, defaultCollectors(), capture.Options{Name: defaultDoctorA})
	if err != nil {
		return nil, nil, err
	}
	return res.Snapshot, imported.Snapshot, nil
}

// loadForDoctor loads a snapshot and, when it is one of the defaults, says how
// to create it. A first-time user reaching for `nyrvo doctor` has captured
// nothing yet, and "snapshot not found" alone leaves them guessing.
func loadForDoctor(store *snapshot.Store, name string) (*snapshot.Snapshot, error) {
	snap, err := store.Load(name)
	if err == nil {
		return snap, nil
	}
	if !errors.Is(err, snapshot.ErrNotFound) {
		return nil, err
	}
	switch name {
	case defaultDoctorA:
		return nil, fmt.Errorf("%w; capture this machine first:\n  nyrvo capture %s", err, defaultDoctorA)
	case defaultDoctorB:
		return nil, fmt.Errorf("%w; capture a CI job first:\n  nyrvo ci inspect\n  nyrvo ci capture <job>", err)
	default:
		return nil, err
	}
}
