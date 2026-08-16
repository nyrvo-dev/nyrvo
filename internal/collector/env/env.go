// Package env records which environment variables are set.
//
// Nyrvo must never persist environment variable values: a snapshot is a file
// users paste into bug reports, so a stored value would leak credentials. This
// collector therefore keeps names only.
package env

import (
	"context"
	"os"
	"sort"
	"strings"

	"github.com/nyrvo-dev/nyrvo/internal/collector"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// Env is the environment-variable collector. Values are deliberately never
// read beyond name extraction: presence alone is enough for drift detection
// ("REDIS_URL is missing in CI"), while persisting values would write
// credentials into a file users share in bug reports and commit by accident.
// See docs/adr/0003-never-store-environment-values.md.
type Env struct {
	// Environ returns the raw "NAME=VALUE" entries to inspect. It defaults to
	// os.Environ when nil, which lets tests inject a fixed environment instead
	// of mutating the real process environment.
	Environ func() []string
}

// Compile-time check that Env satisfies the collector contract.
var _ collector.Collector = Env{}

// Name identifies this collector in progress output and errors.
func (Env) Name() string { return "environment" }

// Collect writes the sorted, de-duplicated set of environment variable names
// into snap.Environment. Only names are stored: a value must never be
// persisted, logged, truncated, hashed, or included in an error message.
func (e Env) Collect(_ context.Context, snap *snapshot.Snapshot) error {
	// ctx is unused by design: this collector spawns no external tools and
	// only transforms data already in memory, so there is nothing to cancel
	// and nothing that can fail.
	raw := os.Environ
	if e.Environ != nil {
		raw = e.Environ
	}
	entries := raw()

	seen := make(map[string]struct{}, len(entries))
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		eq := strings.IndexByte(entry, '=')
		// An entry without '=' carries no name, and one starting with '=' has
		// an empty name (platforms differ in how they render an empty key);
		// both are unobservable variables, so skip them.
		if eq <= 0 {
			continue
		}
		// Split on the first '=' only, so "A=b=c" yields name "A".
		name := entry[:eq]
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	// Canonical order keeps two captures of the same machine byte-identical.
	sort.Strings(names)

	snap.Environment = &snapshot.Environment{Names: names}
	return nil
}
