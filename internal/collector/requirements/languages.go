package requirements

import (
	"encoding/json"
	"strings"

	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// globalJSONReqs reads the SDK version a .NET project pins in global.json.
//
// sdk.version is the version of the SDK the .NET muxer must select. It is a
// floor, not a pin: the resolver rolls forward to a newer SDK whenever the
// exact one is missing. With no rollForward key the default policy is "patch",
// which accepts the latest installed patch of the same feature band, and every
// other policy accepts a newer SDK in some scope. Recording it as a pin
// reproduces the go directive mistake — every machine with a slightly newer SDK
// than the file names reports itself broken, which is the normal arrangement.
//
// rollForward: "disable" is the one value that forbids rolling forward: the
// muxer demands the exact SDK and the build genuinely fails without it, so that
// spelling is recorded as a pin. Every other value, including ones Nyrvo has
// not seen yet, stays a floor — never convicting a machine that might build.
func globalJSONReqs(dir string) []snapshot.Requirement {
	body, ok := readSource(dir, "global.json")
	if !ok {
		return nil
	}
	// Decoded into a struct holding exactly the fields Nyrvo reads, like every
	// other JSON source: the file is untrusted repository input.
	var gj struct {
		SDK struct {
			Version     string `json:"version"`
			RollForward string `json:"rollForward"`
		} `json:"sdk"`
	}
	if err := json.Unmarshal(body, &gj); err != nil {
		// A malformed global.json costs this one source, never the capture.
		return nil
	}
	version := strings.TrimSpace(gj.SDK.Version)
	if version == "" {
		// An sdk block without a version pins nothing: the muxer then uses the
		// highest installed SDK, which is no constraint at all.
		return nil
	}
	return []snapshot.Requirement{{
		Runtime:    "dotnet",
		Constraint: version,
		Source:     "global.json sdk.version",
		Minimum:    strings.TrimSpace(gj.SDK.RollForward) != "disable",
	}}
}

// packageManagerReq reads the packageManager field, which corepack uses to
// select the exact package-manager version to run. The value is
// "<name>@<version>" and may carry a "+sha512.<hex>" integrity suffix that is
// not part of the version, so it is stripped before recording.
//
// It is a PIN, not a floor: corepack downloads and runs precisely that version
// and refuses anything else, so a machine with any other version genuinely
// cannot build this project. This is the opposite of the go directive in go.mod
// and of global.json sdk.version — both roll forward to a newer installed
// toolchain, and this repo has already recorded both as pins before learning
// that. Minimum stays false so the exact version is enforced, and it must not
// be "fixed" into a floor.
//
// Only npm, pnpm and yarn are understood; corepack knows no others. A different
// name, a value without the "@" that separates name and version, or an empty
// version, is a constraint Nyrvo cannot read — and ADR 0012 says such a
// constraint produces no requirement rather than a guess.
func packageManagerReq(pm string) *snapshot.Requirement {
	name, value, ok := strings.Cut(strings.TrimSpace(pm), "@")
	if !ok {
		return nil
	}
	switch name {
	case "npm", "pnpm", "yarn":
	default:
		return nil
	}
	if i := strings.IndexByte(value, '+'); i >= 0 {
		value = value[:i]
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &snapshot.Requirement{
		Runtime:    name,
		Constraint: value,
		Source:     "package.json packageManager",
	}
}

// rubyVersionReqs drops ruby-'s selector syntax because runtime observations
// contain only the version that selector resolves to.
func rubyVersionReqs(dir string) []snapshot.Requirement {
	body, ok := readSource(dir, ".ruby-version")
	if !ok {
		return nil
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		constraint := strings.TrimPrefix(line, "ruby-")
		if constraint != "" {
			return []snapshot.Requirement{{Runtime: "ruby", Constraint: constraint, Source: ".ruby-version"}}
		}
	}
	return nil
}

// gemfileReqs accepts only the quoted form because file: delegates the version
// declaration to another source and must not become a constraint itself.
func gemfileReqs(dir string) []snapshot.Requirement {
	body, ok := readSource(dir, "Gemfile")
	if !ok {
		return nil
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.HasPrefix(line, "ruby") {
			continue
		}
		rest := line[len("ruby"):]
		if rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
			continue
		}
		rest = strings.TrimSpace(rest)
		if len(rest) < 2 || (rest[0] != '\'' && rest[0] != '"') {
			continue
		}
		if end := strings.IndexByte(rest[1:], rest[0]); end >= 0 {
			constraint := strings.TrimSpace(rest[1 : end+1])
			if constraint != "" {
				return []snapshot.Requirement{{Runtime: "ruby", Constraint: constraint, Source: "Gemfile ruby directive"}}
			}
		}
	}
	return nil
}

// composerJSONReqs decodes only the relevant field so unrelated Composer
// configuration cannot affect requirement collection.
func composerJSONReqs(dir string) []snapshot.Requirement {
	body, ok := readSource(dir, "composer.json")
	if !ok {
		return nil
	}
	var composer struct {
		Require struct {
			PHP string `json:"php"`
		} `json:"require"`
	}
	if err := json.Unmarshal(body, &composer); err != nil {
		return nil
	}
	if constraint := strings.TrimSpace(composer.Require.PHP); constraint != "" {
		return []snapshot.Requirement{{Runtime: "php", Constraint: constraint, Source: "composer.json require.php"}}
	}
	return nil
}

// rustReqs keeps rustup's selected channel separate from Cargo's compatibility
// floor because both declarations can coexist and mean different things.
func rustReqs(dir string) []snapshot.Requirement {
	var reqs []snapshot.Requirement
	for _, name := range []string{"rust-toolchain.toml", "rust-toolchain"} {
		body, ok := readSource(dir, name)
		if !ok {
			continue
		}
		if constraint := rustToolchainConstraint(body); constraint != "" {
			reqs = append(reqs, snapshot.Requirement{Runtime: "rust", Constraint: constraint, Source: name})
		}
		// rustup gives the modern filename precedence when both are present.
		break
	}

	body, ok := readSource(dir, "Cargo.toml")
	if !ok {
		return reqs
	}
	inPackage := false
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(stripTOMLComment(line))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inPackage = line == "[package]"
			continue
		}
		if !inPackage {
			continue
		}
		if constraint, ok := quotedAssignment(line, "rust-version"); ok && constraint != "" {
			reqs = append(reqs, snapshot.Requirement{
				Runtime:    "rust",
				Constraint: constraint,
				Source:     "Cargo.toml rust-version",
				Minimum:    true,
			})
			break
		}
	}
	return reqs
}

func rustToolchainConstraint(body []byte) string {
	var meaningful []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(stripTOMLComment(line))
		if line == "" {
			continue
		}
		meaningful = append(meaningful, line)
		if constraint, ok := quotedAssignment(line, "channel"); ok {
			if isNamedRustChannel(constraint) {
				return ""
			}
			return constraint
		}
	}
	if len(meaningful) == 1 && !strings.ContainsAny(meaningful[0], "=[]") && !isNamedRustChannel(meaningful[0]) {
		return meaningful[0]
	}
	return ""
}

func isNamedRustChannel(constraint string) bool {
	switch constraint {
	case "stable", "beta", "nightly":
		return true
	default:
		return false
	}
}

func quotedAssignment(line, key string) (string, bool) {
	left, right, ok := strings.Cut(line, "=")
	if !ok || strings.TrimSpace(left) != key {
		return "", false
	}
	right = strings.TrimSpace(right)
	if len(right) < 2 || (right[0] != '\'' && right[0] != '"') {
		return "", false
	}
	end := strings.IndexByte(right[1:], right[0])
	if end < 0 {
		return "", false
	}
	return strings.TrimSpace(right[1 : end+1]), true
}

// pythonPyprojectReqs reads the Python version a project requires from
// pyproject.toml's [project] table, where PEP 621 puts requires-python.
//
// It is NOT a floor. Minimum exists for declarations like the go directive in
// go.mod and Cargo.toml's rust-version, which state a bare version that
// implicitly means "or newer". requires-python is not that: it carries its own
// explicit operators — ">=3.11" or ">=3.11,<3.14" — and the diagnostic layer
// evaluates them directly. Marking it Minimum as well would layer a second,
// implicit "or newer" on top of a constraint that already says exactly what it
// means, and would silently discard an upper bound. This is the same shape as
// composer.json's "^8.3", which is recorded with Minimum false. So Minimum
// stays false and the operators are enforced verbatim.
//
// The table match is exact, the way rustReqs compares to "[package]":
// [project.optional-dependencies] and [project.urls] are different tables and
// must never be mistaken for [project]. pyproject.toml is untrusted repository
// input, so only the one quoted assignment is read and anything unparseable
// yields nothing rather than a guess, per ADR 0012.
func pythonPyprojectReqs(dir string) []snapshot.Requirement {
	body, ok := readSource(dir, "pyproject.toml")
	if !ok {
		return nil
	}
	inProject := false
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(stripTOMLComment(line))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inProject = line == "[project]"
			continue
		}
		if !inProject {
			continue
		}
		if constraint, ok := quotedAssignment(line, "requires-python"); ok && constraint != "" {
			return []snapshot.Requirement{{
				Runtime:    "python",
				Constraint: constraint,
				Source:     "pyproject.toml requires-python",
			}}
		}
	}
	return nil
}

func stripTOMLComment(line string) string {
	quote := byte(0)
	for i := range len(line) {
		switch line[i] {
		case '\'', '"':
			switch quote {
			case 0:
				quote = line[i]
			case line[i]:
				quote = 0
			}
		case '#':
			if quote == 0 {
				return line[:i]
			}
		}
	}
	return line
}

// javaVersionReqs removes vendor text because the Java probe reports the
// numeric runtime version independently of its distribution.
func javaVersionReqs(dir string) []snapshot.Requirement {
	body, ok := readSource(dir, ".java-version")
	if !ok {
		return nil
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for i := range len(line) {
			if line[i] >= '0' && line[i] <= '9' {
				return []snapshot.Requirement{{Runtime: "java", Constraint: line[i:], Source: ".java-version"}}
			}
		}
	}
	return nil
}

// extraToolVersionAliases exposes only aliases whose versions describe the
// same runtime; installer versions such as rustup would be misleading.
func extraToolVersionAliases() map[string]string {
	return map[string]string{
		"java":    "java",
		"jdk":     "java",
		"openjdk": "java",
		"php":     "php",
		"ruby":    "ruby",
		"rust":    "rust",
	}
}
