package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"

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

func runDoctor(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "write the diagnosis as JSON")
	flags, operands := splitFlags(args)
	if err := fs.Parse(flags); err != nil {
		return usageErr("doctor: %v", err)
	}

	a, b := defaultDoctorA, defaultDoctorB
	switch len(operands) {
	case 0:
	case 2:
		a, b = operands[0], operands[1]
	default:
		return usageErr("doctor takes two snapshot names, or none to diagnose %s against %s", defaultDoctorA, defaultDoctorB)
	}

	store := snapshot.NewStore("")
	snapA, err := loadForDoctor(store, a)
	if err != nil {
		return err
	}
	snapB, err := loadForDoctor(store, b)
	if err != nil {
		return err
	}

	// The diff is computed and discarded here: doctor reports conclusions, and
	// a user who wants the underlying evidence runs `nyrvo diff`. Keeping the
	// two commands separate is what lets someone who disagrees with a rule
	// still trust the comparison beneath it.
	_, findings := diagnostic.Analyze(snapA, snapB)

	if *asJSON {
		return output.DoctorJSON(stdout, a, b, findings)
	}
	return output.DoctorText(stdout, a, b, findings)
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
