package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/nyrvo-dev/nyrvo/internal/capture"
	"github.com/nyrvo-dev/nyrvo/internal/ci/githubactions"
	"github.com/nyrvo-dev/nyrvo/internal/diagnostic"
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
	flags, operands := splitFlags(args)
	if err := fs.Parse(flags); err != nil {
		return usageErr("doctor: %v", err)
	}

	a, b := defaultDoctorA, defaultDoctorB
	var snapA, snapB *snapshot.Snapshot
	var err error

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

	// The diff is computed and discarded here: doctor reports conclusions, and
	// a user who wants the underlying evidence runs `nyrvo diff`. Keeping the
	// two commands separate is what lets someone who disagrees with a rule
	// still trust the comparison beneath it.
	_, findings := diagnostic.Analyze(snapA, snapB)

	// A snapshot's own notes — the run's conclusion, the step that failed, what
	// Nyrvo could not model — answer "why did this fail?" in a way no
	// environment rule can. Without them a diagnosis of a failed run would list
	// two low-severity platform differences and never mention the failure.
	context := evidenceContext(snapA, snapB)

	if *asJSON {
		return output.DoctorJSON(stdout, a, b, findings, context...)
	}
	return output.DoctorText(stdout, a, b, findings, context...)
}

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
