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
