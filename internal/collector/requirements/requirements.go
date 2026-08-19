// Package requirements reads what the checked-out project says it needs.
//
// Nyrvo can already say two environments differ; it cannot yet say one of them
// is wrong. A project that declares "engines": {"node": ">=24"} and a CI job
// that installs Node 22 is not a difference of opinion, it is a broken setup —
// and that is the difference between a finding worth acting on and a line of
// trivia. This collector reads those declarations. It does not evaluate them:
// judging a constraint against an observed runtime is the job of a later rule,
// and keeping the reading separate from the judging is what lets each be tested
// alone.
package requirements

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nyrvo-dev/nyrvo/internal/collector"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// maxSourceSize caps how much of one project file is read. package.json is
// untrusted input from the repository: a generated or hostile file must not
// exhaust memory just because Nyrvo glanced at it. One megabyte is far beyond
// anything these files legitimately contain.
const maxSourceSize = 1 << 20 // 1 MiB

// Requirements collects the version constraints a project declares.
type Requirements struct {
	// Dir is the project directory to read. Empty means the process working
	// directory, which is the common case when capturing the environment the
	// user is standing in.
	Dir string
}

// Compile-time check that Requirements satisfies the collector contract.
var _ collector.Collector = Requirements{}

// Name identifies this collector in progress output and errors.
func (Requirements) Name() string { return "requirements" }

// Collect appends the declared requirements to snap.Requirements.
//
// Every source is optional: a project may declare nothing, a source may be
// absent, and one broken file must not cost the user every other requirement.
// When nothing at all was declared the section is recorded as absent via
// ErrUnavailable rather than as an empty list, which would look like "the
// project needs no version pinned" — a claim this collector never made.
func (r Requirements) Collect(_ context.Context, snap *snapshot.Snapshot) error {
	// ctx is unused by design: this collector spawns no external tools and only
	// reads local files, so there is nothing to cancel.
	dir := r.Dir
	if dir == "" {
		// Empty Dir means "the project I am standing in"; resolve it once here
		// so every source below reads from the same place.
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve working directory: %w", err)
		}
		dir = wd
	}

	reqs := packageJSONReqs(dir)
	reqs = append(reqs, goModReqs(dir)...)
	reqs = append(reqs, nvmrcReqs(dir)...)
	reqs = append(reqs, pythonVersionReqs(dir)...)
	reqs = append(reqs, pythonPyprojectReqs(dir)...)
	reqs = append(reqs, toolVersionsReqs(dir)...)
	reqs = append(reqs, rubyVersionReqs(dir)...)
	reqs = append(reqs, gemfileReqs(dir)...)
	reqs = append(reqs, composerJSONReqs(dir)...)
	reqs = append(reqs, rustReqs(dir)...)
	reqs = append(reqs, javaVersionReqs(dir)...)
	reqs = append(reqs, globalJSONReqs(dir)...)

	if len(reqs) == 0 {
		return fmt.Errorf("no requirement sources in %s: %w", dir, collector.ErrUnavailable)
	}

	// Existing entries survive: deduplication and sorting run over the combined
	// set so a re-collection on an already-filled snapshot adds nothing new.
	snap.Requirements = dedupeAndSort(append(snap.Requirements, reqs...))
	return nil
}

// readSource reads a file inside dir, returning false when the file is absent,
// unreadable, or too large to be worth trusting. Absence is normal and must not
// look like a failure, so callers treat a false the same way: skip the source.
func readSource(dir, name string) ([]byte, bool) {
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return nil, false
	}
	// The file is only read, so a failed close carries no lost data to report.
	defer func() { _ = f.Close() }()
	// LimitReader keeps the allocation bounded to the cap: an oversized file
	// costs us one byte of evidence past the cap, not a second copy of its full
	// content, and never gets parsed.
	body, err := io.ReadAll(io.LimitReader(f, maxSourceSize+1))
	if err != nil || len(body) > maxSourceSize {
		return nil, false
	}
	return body, true
}

// packageJSON reads the engines section and the packageManager field of
// package.json. The file is untrusted repository input, so it is decoded into a
// struct holding exactly the fields Nyrvo reads — never a map walked blindly,
// and nothing is ever executed.
//
// engines.npm is recorded under Runtime "npm" rather than being folded into
// "node": npm's version is independent of Node's and a finding must be able to
// point at the one that is actually wrong. The packageManager field pins the
// exact package-manager version corepack must run and is read by
// packageManagerReq.
func packageJSONReqs(dir string) []snapshot.Requirement {
	body, ok := readSource(dir, "package.json")
	if !ok {
		return nil
	}
	var pj struct {
		PackageManager string `json:"packageManager"`
		Engines        struct {
			Node string `json:"node"`
			NPM  string `json:"npm"`
		} `json:"engines"`
	}
	if err := json.Unmarshal(body, &pj); err != nil {
		// A malformed package.json costs this one source, never the capture.
		return nil
	}
	var reqs []snapshot.Requirement
	if v := strings.TrimSpace(pj.Engines.Node); v != "" {
		reqs = append(reqs, snapshot.Requirement{Runtime: "node", Constraint: v, Source: "package.json engines.node"})
	}
	if v := strings.TrimSpace(pj.Engines.NPM); v != "" {
		reqs = append(reqs, snapshot.Requirement{Runtime: "npm", Constraint: v, Source: "package.json engines.npm"})
	}
	if req := packageManagerReq(pj.PackageManager); req != nil {
		reqs = append(reqs, *req)
	}
	return reqs
}

// goModReqs reads the go directive (e.g. `go 1.25`).
//
// Since Go 1.21 the directive is the lowest version the module accepts, not a
// pin: building with a newer toolchain satisfies it. It is recorded as a
// minimum for that reason — read as a pin, every project developed on a Go
// newer than the one it supports reports itself as broken, which is the normal
// and intended arrangement.
//
// The toolchain directive is deliberately ignored: it names the compiler Nyrvo
// would need, not the language level the project declares it requires.
func goModReqs(dir string) []snapshot.Requirement {
	body, ok := readSource(dir, "go.mod")
	if !ok {
		return nil
	}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "go" {
			return []snapshot.Requirement{{
				Runtime:    "go",
				Constraint: fields[1],
				Source:     "go.mod go directive",
				Minimum:    true,
			}}
		}
	}
	return nil
}

// nvmrcReqs reads the Node version pinned in .nvmrc. Only the first meaningful
// line counts; the file has no structure beyond that one value. The leading
// "v" nvm versions often carry is presentation, not part of the version.
func nvmrcReqs(dir string) []snapshot.Requirement {
	body, ok := readSource(dir, ".nvmrc")
	if !ok {
		return nil
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		constraint := strings.TrimPrefix(line, "v")
		if constraint == "" {
			continue
		}
		return []snapshot.Requirement{{Runtime: "node", Constraint: constraint, Source: ".nvmrc"}}
	}
	return nil
}

// pythonVersionReqs reads the Python version pinned in .python-version. pyenv
// accepts several lines, but only the first is honored — matching which version
// pyenv would actually select.
func pythonVersionReqs(dir string) []snapshot.Requirement {
	body, ok := readSource(dir, ".python-version")
	if !ok {
		return nil
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return []snapshot.Requirement{{Runtime: "python", Constraint: line, Source: ".python-version"}}
	}
	return nil
}

// toolVersionsReqs reads asdf/mise declarations from .tool-versions. Only tools
// Nyrvo models are kept: anything else is ignored rather than inventing a
// runtime name the rest of the pipeline cannot match.
func toolVersionsReqs(dir string) []snapshot.Requirement {
	body, ok := readSource(dir, ".tool-versions")
	if !ok {
		return nil
	}
	// asdf and mise both accept tool aliases; normalize only the known ones and
	// keep the source identical so a duplicate alias resolves to one entry.
	runtimeFor := map[string]string{
		"nodejs":      "node",
		"node":        "node",
		"golang":      "go",
		"go":          "go",
		"python":      "python",
		"npm":         "npm",
		"pnpm":        "pnpm",
		"yarn":        "yarn",
		"composer":    "composer",
		"dotnet":      "dotnet",
		"dotnet-core": "dotnet",
	}
	for alias, runtime := range extraToolVersionAliases() {
		runtimeFor[alias] = runtime
	}
	var reqs []snapshot.Requirement
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		runtime, ok := runtimeFor[fields[0]]
		if !ok {
			continue
		}
		reqs = append(reqs, snapshot.Requirement{Runtime: runtime, Constraint: fields[1], Source: ".tool-versions"})
	}
	return reqs
}

// dedupeAndSort collapses (Runtime, Source) pairs and orders the remainder the
// way snapshot.Normalize would, so the appended section stays canonical even
// when the snapshot already carried entries. The same runtime from different
// sources is kept on purpose: .nvmrc and package.json disagreeing is itself a
// fact worth seeing.
func dedupeAndSort(reqs []snapshot.Requirement) []snapshot.Requirement {
	seen := make(map[string]struct{}, len(reqs))
	out := reqs[:0]
	for _, r := range reqs {
		key := r.Runtime + "\x00" + r.Source
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Runtime != out[j].Runtime {
			return out[i].Runtime < out[j].Runtime
		}
		return out[i].Source < out[j].Source
	})
	return out
}
