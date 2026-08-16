package requirements

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/collector"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func collect(t *testing.T, dir string, existing ...snapshot.Requirement) (*snapshot.Snapshot, error) {
	t.Helper()
	snap := snapshot.New("test", time.Now())
	snap.Requirements = existing
	err := (Requirements{Dir: dir}).Collect(context.Background(), snap)
	return snap, err
}

func req(runtime, constraint, source string) snapshot.Requirement {
	return snapshot.Requirement{Runtime: runtime, Constraint: constraint, Source: source}
}

func findReq(t *testing.T, reqs []snapshot.Requirement, runtime, source string) string {
	t.Helper()
	for _, r := range reqs {
		if r.Runtime == runtime && r.Source == source {
			return r.Constraint
		}
	}
	t.Fatalf("no requirement with runtime %q and source %q in %v", runtime, source, reqs)
	return ""
}

func TestName(t *testing.T) {
	if got := (Requirements{}).Name(); got != "requirements" {
		t.Fatalf("Name() = %q, want %q", got, "requirements")
	}
}

func TestPackageJSONEngines(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"name":"app","engines":{"node":">=24","npm":"^10.2"}}`)

	snap, err := collect(t, dir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := []snapshot.Requirement{
		req("node", ">=24", "package.json engines.node"),
		req("npm", "^10.2", "package.json engines.npm"),
	}
	if !reflect.DeepEqual(snap.Requirements, want) {
		t.Fatalf("requirements = %v, want %v", snap.Requirements, want)
	}
}

func TestPackageJSONWithoutEnginesContributesNothing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"name":"app"}`)

	_, err := collect(t, dir)
	if !errors.Is(err, collector.ErrUnavailable) {
		t.Fatalf("Collect err = %v, want ErrUnavailable", err)
	}
}

func TestGoModDirective(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", `module example.com/app

go 1.25

toolchain go1.26.1

require example.com/dep v1.0.0
`)

	snap, err := collect(t, dir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	// The toolchain and require lines must not be mistaken for the go directive.
	want := []snapshot.Requirement{req("go", "1.25", "go.mod go directive")}
	if !reflect.DeepEqual(snap.Requirements, want) {
		t.Fatalf("requirements = %v, want %v", snap.Requirements, want)
	}
}

func TestNvmrcStripsV(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".nvmrc", "v20.11.1\n")

	snap, err := collect(t, dir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := []snapshot.Requirement{req("node", "20.11.1", ".nvmrc")}
	if !reflect.DeepEqual(snap.Requirements, want) {
		t.Fatalf("requirements = %v, want %v", snap.Requirements, want)
	}
}

func TestPythonVersionFirstLineOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".python-version", "3.11\n3.12\n3.13\n")

	snap, err := collect(t, dir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := []snapshot.Requirement{req("python", "3.11", ".python-version")}
	if !reflect.DeepEqual(snap.Requirements, want) {
		t.Fatalf("requirements = %v, want %v", snap.Requirements, want)
	}
}

func TestToolVersionsKnownToolsOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".tool-versions", `nodejs 20.11.1
golang 1.26.1
python 3.11
ruby 3.2.2
terraform 1.9.0
`)

	snap, err := collect(t, dir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	// The two unknown tools are dropped rather than given invented runtime names.
	want := []snapshot.Requirement{
		req("go", "1.26.1", ".tool-versions"),
		req("node", "20.11.1", ".tool-versions"),
		req("python", "3.11", ".tool-versions"),
	}
	if !reflect.DeepEqual(snap.Requirements, want) {
		t.Fatalf("requirements = %v, want %v", snap.Requirements, want)
	}
}

func TestAllSourcesTogetherSortedAndDeduped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"engines":{"node":">=24","npm":"^10.2"}}`)
	writeFile(t, dir, "go.mod", "module example.com/app\n\ngo 1.25\n")
	writeFile(t, dir, ".nvmrc", "v20.11.1\n")
	writeFile(t, dir, ".python-version", "3.11\n")
	// nodejs and node both map to "node" and must collapse into one entry.
	writeFile(t, dir, ".tool-versions", "nodejs 22.0.0\ngolang 1.26.1\npython 3.12\n")

	snap, err := collect(t, dir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	// Both node entries (package.json and .nvmrc disagreeing) are kept; the two
	// node aliases in .tool-versions collapse to one. Sorted by runtime, then
	// source — "." sorts before letters, so .tool-versions precedes others.
	want := []snapshot.Requirement{
		req("go", "1.26.1", ".tool-versions"),
		req("go", "1.25", "go.mod go directive"),
		req("node", "20.11.1", ".nvmrc"),
		req("node", "22.0.0", ".tool-versions"),
		req("node", ">=24", "package.json engines.node"),
		req("npm", "^10.2", "package.json engines.npm"),
		req("python", "3.11", ".python-version"),
		req("python", "3.12", ".tool-versions"),
	}
	if !reflect.DeepEqual(snap.Requirements, want) {
		t.Fatalf("requirements = %v, want %v", snap.Requirements, want)
	}
}

func TestConstraintsVerbatim(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"engines":{"node":">=24","npm":"^20.1"}}`)
	writeFile(t, dir, "go.mod", "module example.com/app\n\ngo 1.25\n")
	writeFile(t, dir, ".nvmrc", "v20.11.1\n")
	writeFile(t, dir, ".python-version", "~3.11\n")
	writeFile(t, dir, ".tool-versions", "python 3.12\n")

	snap, err := collect(t, dir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, tc := range []struct {
		runtime, source, want string
	}{
		{"node", "package.json engines.node", ">=24"},
		{"npm", "package.json engines.npm", "^20.1"},
		{"go", "go.mod go directive", "1.25"},
		{"node", ".nvmrc", "20.11.1"},
		{"python", ".python-version", "~3.11"},
		{"python", ".tool-versions", "3.12"},
	} {
		if got := findReq(t, snap.Requirements, tc.runtime, tc.source); got != tc.want {
			t.Errorf("%s from %s = %q, want %q", tc.runtime, tc.source, got, tc.want)
		}
	}
}

func TestMalformedPackageJSONStillReadsGoMod(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"engines":{"node":`+"\n") // broken
	writeFile(t, dir, "go.mod", "module example.com/app\n\ngo 1.25\n")

	snap, err := collect(t, dir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := []snapshot.Requirement{req("go", "1.25", "go.mod go directive")}
	if !reflect.DeepEqual(snap.Requirements, want) {
		t.Fatalf("requirements = %v, want %v", snap.Requirements, want)
	}
}

func TestEmptyDirectoryUnavailable(t *testing.T) {
	dir := t.TempDir()

	snap, err := collect(t, dir)
	if !errors.Is(err, collector.ErrUnavailable) {
		t.Fatalf("Collect err = %v, want ErrUnavailable", err)
	}
	// The section is reported absent, never as an empty claim.
	if snap.Requirements != nil {
		t.Fatalf("requirements = %v, want untouched nil", snap.Requirements)
	}
}

func TestPreExistingRequirementsPreserved(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/app\n\ngo 1.25\n")

	existing := []snapshot.Requirement{req("dotnet", "8.0", "test fixture")}
	snap, err := collect(t, dir, existing...)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(snap.Requirements) != 2 {
		t.Fatalf("len(requirements) = %d, want 2: %v", len(snap.Requirements), snap.Requirements)
	}
	if got := findReq(t, snap.Requirements, "dotnet", "test fixture"); got != "8.0" {
		t.Fatalf("pre-existing requirement lost or changed: %v", snap.Requirements)
	}
	if got := findReq(t, snap.Requirements, "go", "go.mod go directive"); got != "1.25" {
		t.Fatalf("new requirement missing: %v", snap.Requirements)
	}
}

func TestCollectTwiceDoesNotDuplicate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".nvmrc", "v20.11.1\n")

	snap, err := collect(t, dir)
	if err != nil {
		t.Fatalf("first Collect: %v", err)
	}
	// A second collection over the same snapshot adds nothing new.
	if err := (Requirements{Dir: dir}).Collect(context.Background(), snap); err != nil {
		t.Fatalf("second Collect: %v", err)
	}
	want := []snapshot.Requirement{req("node", "20.11.1", ".nvmrc")}
	if !reflect.DeepEqual(snap.Requirements, want) {
		t.Fatalf("requirements = %v, want %v", snap.Requirements, want)
	}
}

func TestCommentsAndBlankLinesSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".nvmrc", "\n# pinned via asdf\n\nv20.11.1\n")
	writeFile(t, dir, ".python-version", "\n# system default\n3.11\n3.12\n")
	writeFile(t, dir, ".tool-versions", "# generated\n\nruby 3.2.2\nnodejs 20.11.1\n\n")

	snap, err := collect(t, dir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := []snapshot.Requirement{
		req("node", "20.11.1", ".nvmrc"),
		req("node", "20.11.1", ".tool-versions"),
		req("python", "3.11", ".python-version"),
	}
	if !reflect.DeepEqual(snap.Requirements, want) {
		t.Fatalf("requirements = %v, want %v", snap.Requirements, want)
	}
}

func TestReadSourceAbsentFile(t *testing.T) {
	dir := t.TempDir()

	if body, ok := readSource(dir, "package.json"); ok {
		t.Fatalf("readSource found package.json, body = %q", body)
	}
}

func TestReadSourceCapsSize(t *testing.T) {
	dir := t.TempDir()
	// One byte over the cap: reading it must be refused, not accepted and held.
	big := strings.Repeat("x", maxSourceSize+1)
	writeFile(t, dir, "package.json", big)

	if body, ok := readSource(dir, "package.json"); ok {
		t.Fatalf("readSource accepted a %d-byte file over the %d-byte cap", len(big), maxSourceSize)
	} else if body != nil {
		t.Fatalf("readSource returned %d bytes for an oversized file", len(body))
	}
}

func TestOversizedPackageJSONSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/app\n\ngo 1.25\n")
	// A valid JSON prefix fattened past the cap. If the collector read the whole
	// file it would either hold megabytes or still parse; neither happens here.
	var b strings.Builder
	b.WriteString(`{"engines":{"node":">=24"},"padding":"`)
	b.WriteString(strings.Repeat("x", maxSourceSize))
	b.WriteString(`"}`)
	writeFile(t, dir, "package.json", b.String())

	snap, err := collect(t, dir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	// The oversized package.json contributes nothing; go.mod still lands.
	want := []snapshot.Requirement{req("go", "1.25", "go.mod go directive")}
	if !reflect.DeepEqual(snap.Requirements, want) {
		t.Fatalf("requirements = %v, want %v", snap.Requirements, want)
	}
}
