package requirements

import (
	"reflect"
	"testing"

	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

func TestLanguageReadersAbsent(t *testing.T) {
	dir := t.TempDir()
	readers := map[string]func(string) []snapshot.Requirement{
		"ruby version": rubyVersionReqs,
		"Gemfile":      gemfileReqs,
		"Composer":     composerJSONReqs,
		"Rust":         rustReqs,
		"Java":         javaVersionReqs,
		"global.json":  globalJSONReqs,
	}
	for name, reader := range readers {
		t.Run(name, func(t *testing.T) {
			if got := reader(dir); got != nil {
				t.Fatalf("reader returned %v, want nil", got)
			}
		})
	}
}

func TestRubyVersionReqs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".ruby-version", "\n# selected by rbenv\nruby-3.3.0\n3.2.0\n")

	want := []snapshot.Requirement{{Runtime: "ruby", Constraint: "3.3.0", Source: ".ruby-version"}}
	if got := rubyVersionReqs(dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("rubyVersionReqs() = %v, want %v", got, want)
	}
}

func TestGemfileReqs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Gemfile", "source \"https://rubygems.org\"\n\nruby '>= 3.1'\n")

	want := []snapshot.Requirement{{Runtime: "ruby", Constraint: ">= 3.1", Source: "Gemfile ruby directive"}}
	if got := gemfileReqs(dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("gemfileReqs() = %v, want %v", got, want)
	}
}

func TestGemfileReqsIgnoresFileAndComment(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Gemfile", "# ruby \"3.3.0\"\nruby file: \".ruby-version\"\n")

	if got := gemfileReqs(dir); got != nil {
		t.Fatalf("gemfileReqs() = %v, want nil", got)
	}
}

func TestComposerJSONReqs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "composer.json", `{"name":"example/app","require":{"php":"^8.2"}}`)

	want := []snapshot.Requirement{{Runtime: "php", Constraint: "^8.2", Source: "composer.json require.php"}}
	if got := composerJSONReqs(dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("composerJSONReqs() = %v, want %v", got, want)
	}
}

func TestComposerJSONReqsIgnoresMissingAndMalformedPHP(t *testing.T) {
	t.Run("missing require.php", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "composer.json", `{"name":"example/app","require":{"ext-json":"*"}}`)
		if got := composerJSONReqs(dir); got != nil {
			t.Fatalf("composerJSONReqs() = %v, want nil", got)
		}
	})
	t.Run("malformed JSON", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "composer.json", `{"require":{"php":`)
		if got := composerJSONReqs(dir); got != nil {
			t.Fatalf("composerJSONReqs() = %v, want nil", got)
		}
	})
}

func TestRustToolchainBareVersionAndNamedChannel(t *testing.T) {
	t.Run("version", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "rust-toolchain", "1.75.0\n")
		want := []snapshot.Requirement{{Runtime: "rust", Constraint: "1.75.0", Source: "rust-toolchain"}}
		if got := rustReqs(dir); !reflect.DeepEqual(got, want) {
			t.Fatalf("rustReqs() = %v, want %v", got, want)
		}
	})
	t.Run("stable", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "rust-toolchain", "stable\n")
		if got := rustReqs(dir); got != nil {
			t.Fatalf("rustReqs() = %v, want nil", got)
		}
	})
}

func TestRustToolchainTOML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "rust-toolchain.toml", "[toolchain]\nchannel = \"1.75.0\"\ncomponents = [\"rustfmt\"]\n")

	want := []snapshot.Requirement{{Runtime: "rust", Constraint: "1.75.0", Source: "rust-toolchain.toml"}}
	if got := rustReqs(dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("rustReqs() = %v, want %v", got, want)
	}
}

func TestCargoRustVersionIsMinimum(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Cargo.toml", "[package]\nname = \"app\"\nrust-version = \"1.75\"\n")

	want := []snapshot.Requirement{{
		Runtime:    "rust",
		Constraint: "1.75",
		Source:     "Cargo.toml rust-version",
		Minimum:    true,
	}}
	if got := rustReqs(dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("rustReqs() = %v, want %v", got, want)
	}
}

func TestCargoRustVersionOutsidePackageIgnored(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Cargo.toml", "[dependencies.foo]\nrust-version = \"1.75\"\n")

	if got := rustReqs(dir); got != nil {
		t.Fatalf("rustReqs() = %v, want nil", got)
	}
}

func TestRustReqsReportsToolchainAndCargo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "rust-toolchain", "1.76.0\n")
	writeFile(t, dir, "Cargo.toml", "[package]\nrust-version = \"1.75\"\n")

	want := []snapshot.Requirement{
		{Runtime: "rust", Constraint: "1.76.0", Source: "rust-toolchain"},
		{Runtime: "rust", Constraint: "1.75", Source: "Cargo.toml rust-version", Minimum: true},
	}
	if got := rustReqs(dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("rustReqs() = %v, want %v", got, want)
	}
}

func TestJavaVersionReqs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".java-version", "\n# SDKMAN distribution\ntemurin-21.0.1\n")

	want := []snapshot.Requirement{{Runtime: "java", Constraint: "21.0.1", Source: ".java-version"}}
	if got := javaVersionReqs(dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("javaVersionReqs() = %v, want %v", got, want)
	}
}

func TestPackageManagerReq(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want *snapshot.Requirement
	}{
		{"pnpm", "pnpm@10.33.0", &snapshot.Requirement{Runtime: "pnpm", Constraint: "10.33.0", Source: "package.json packageManager"}},
		{"pnpm with integrity suffix", "pnpm@10.33.0+sha512.1234567890abcdef", &snapshot.Requirement{Runtime: "pnpm", Constraint: "10.33.0", Source: "package.json packageManager"}},
		{"npm", "npm@9.8.1", &snapshot.Requirement{Runtime: "npm", Constraint: "9.8.1", Source: "package.json packageManager"}},
		{"yarn", "yarn@4.6.0", &snapshot.Requirement{Runtime: "yarn", Constraint: "4.6.0", Source: "package.json packageManager"}},
		{"unknown package manager", "bun@1.1.0", nil},
		{"no @ separator", "pnpm10.33.0", nil},
		{"empty version", "pnpm@", nil},
		{"empty field", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := packageManagerReq(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("packageManagerReq(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestPackageManagerIsAPinNotAFloor(t *testing.T) {
	// Corepack downloads and runs precisely the version packageManager names
	// and refuses anything else, so the requirement must enforce the exact
	// version. This is the opposite of the go directive and of global.json
	// sdk.version, both of which roll forward — and both of which this repo once
	// recorded as pins before learning the difference.
	req := packageManagerReq("pnpm@10.33.0+sha512.abc")
	if req == nil {
		t.Fatal("packageManagerReq returned nil")
	}
	if req.Minimum {
		t.Error("packageManager is a pin, not a floor; Minimum must stay false")
	}
	if req.Constraint != "10.33.0" {
		t.Errorf("Constraint = %q, want %q", req.Constraint, "10.33.0")
	}
}

func TestGlobalJSONReqs(t *testing.T) {
	t.Run("version is a floor", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "global.json", `{"sdk":{"version":"9.0.100","rollForward":"latestFeature"}}`)

		want := []snapshot.Requirement{{
			Runtime:    "dotnet",
			Constraint: "9.0.100",
			Source:     "global.json sdk.version",
			Minimum:    true,
		}}
		if got := globalJSONReqs(dir); !reflect.DeepEqual(got, want) {
			t.Fatalf("globalJSONReqs() = %v, want %v", got, want)
		}
	})
	t.Run("default rollForward is still a floor", func(t *testing.T) {
		// With no rollForward key the resolver uses the "patch" policy, which
		// accepts a newer patch of the same feature band, so a newer SDK on the
		// machine still builds this project.
		dir := t.TempDir()
		writeFile(t, dir, "global.json", `{"sdk":{"version":"9.0.100"}}`)

		if got := globalJSONReqs(dir); len(got) != 1 || !got[0].Minimum {
			t.Fatalf("globalJSONReqs() = %v, want one floor requirement", got)
		}
	})
	t.Run("rollForward disable is a pin", func(t *testing.T) {
		// disable forbids rolling forward: the muxer demands the exact SDK and
		// a machine with any other version genuinely cannot build the project.
		dir := t.TempDir()
		writeFile(t, dir, "global.json", `{"sdk":{"version":"8.0.302","rollForward":"disable"}}`)

		want := []snapshot.Requirement{{
			Runtime:    "dotnet",
			Constraint: "8.0.302",
			Source:     "global.json sdk.version",
		}}
		if got := globalJSONReqs(dir); !reflect.DeepEqual(got, want) {
			t.Fatalf("globalJSONReqs() = %v, want %v", got, want)
		}
	})
}

func TestGlobalJSONReqsMalformedOrWithoutSDKVersion(t *testing.T) {
	t.Run("malformed JSON", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "global.json", `{"sdk":{"version":`)
		if got := globalJSONReqs(dir); got != nil {
			t.Fatalf("globalJSONReqs() = %v, want nil", got)
		}
	})
	t.Run("no sdk key", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "global.json", `{"msbuild-sdks":{"Foo":"1.0.0"}}`)
		if got := globalJSONReqs(dir); got != nil {
			t.Fatalf("globalJSONReqs() = %v, want nil", got)
		}
	})
	t.Run("sdk without version", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "global.json", `{"sdk":{"rollForward":"latestMinor"}}`)
		if got := globalJSONReqs(dir); got != nil {
			t.Fatalf("globalJSONReqs() = %v, want nil", got)
		}
	})
}

func TestPyprojectRequiresPython(t *testing.T) {
	// Real-shaped manifests, including both spellings that occur in the wild:
	// spaces around the operators (">= 3.11, < 3.13.0a1") and a plain
	// comma-separated range (">=3.11,<3.14").
	tests := []struct {
		name      string
		pyproject string
		want      string
	}{
		{"plain floor", "[project]\nname = \"app\"\nrequires-python = \">=3.10\"\n", ">=3.10"},
		{"comma-separated range", "[project]\nname = \"app\"\nrequires-python = \">=3.11,<3.14\"\n", ">=3.11,<3.14"},
		{"spaces around operators", "[project]\nname = \"app\"\nrequires-python = \">= 3.11, < 3.13.0a1\"\n", ">= 3.11, < 3.13.0a1"},
		{"single quotes", "[project]\nname = \"app\"\nrequires-python = '>=3.10'\n", ">=3.10"},
		{"comment on the assignment", "[project]\nname = \"app\"\nrequires-python = \">=3.11\"  # floor\n", ">=3.11"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "pyproject.toml", tt.pyproject)

			want := []snapshot.Requirement{{Runtime: "python", Constraint: tt.want, Source: "pyproject.toml requires-python"}}
			if got := pythonPyprojectReqs(dir); !reflect.DeepEqual(got, want) {
				t.Fatalf("pythonPyprojectReqs() = %v, want %v", got, want)
			}
		})
	}
}

func TestPyprojectRequiresPythonIsNotMinimum(t *testing.T) {
	// requires-python carries its own operators, so it must not be treated as a
	// floor the way go.mod and Cargo.toml's rust-version are. Minimum staying
	// false is what lets the constraint engine enforce the declared upper bound.
	dir := t.TempDir()
	writeFile(t, dir, "pyproject.toml", "[project]\nname = \"app\"\nrequires-python = \">=3.11,<3.14\"\n")

	got := pythonPyprojectReqs(dir)
	if len(got) != 1 {
		t.Fatalf("pythonPyprojectReqs() = %v, want one requirement", got)
	}
	if got[0].Minimum {
		t.Error("requires-python is not a floor; Minimum must stay false")
	}
	if got[0].Constraint != ">=3.11,<3.14" {
		t.Errorf("Constraint = %q, want %q", got[0].Constraint, ">=3.11,<3.14")
	}
}

func TestPyprojectRequiresPythonOnlyInProjectTable(t *testing.T) {
	// A decoy line in [project.optional-dependencies] must never be read as the
	// [project] table's requires-python: the table match is exact.
	dir := t.TempDir()
	writeFile(t, dir, "pyproject.toml", `[project]
name = "app"
requires-python = ">=3.11"

[project.optional-dependencies]
dev = ["requires-python = \">=99\""]
`)

	if got := pythonPyprojectReqs(dir); len(got) != 1 {
		t.Fatalf("pythonPyprojectReqs() = %v, want one requirement", got)
	} else if got[0].Constraint != ">=3.11" {
		t.Fatalf("Constraint = %q, want %q (decoy table was read)", got[0].Constraint, ">=3.11")
	}
}

func TestPyprojectBothPythonVersionAndPyproject(t *testing.T) {
	// A project may carry both .python-version and pyproject.toml. They are
	// different sources making different claims, and collapsing them would hide
	// a project whose two files disagree — which is itself worth seeing.
	dir := t.TempDir()
	writeFile(t, dir, ".python-version", "3.11.3\n")
	writeFile(t, dir, "pyproject.toml", "[project]\nname = \"app\"\nrequires-python = \">=3.11,<3.14\"\n")

	want := []snapshot.Requirement{
		{Runtime: "python", Constraint: "3.11.3", Source: ".python-version"},
		{Runtime: "python", Constraint: ">=3.11,<3.14", Source: "pyproject.toml requires-python"},
	}
	if got := pythonVersionReqs(dir); len(got) != 1 || !reflect.DeepEqual(got[0], want[0]) {
		t.Fatalf("pythonVersionReqs() = %v, want %v", got, want[0])
	}
	if got := pythonPyprojectReqs(dir); !reflect.DeepEqual(got, want[1:]) {
		t.Fatalf("pythonPyprojectReqs() = %v, want %v", got, want[1:])
	}
}

func TestPyprojectRequiresPythonMalformedYieldsNothing(t *testing.T) {
	t.Run("absent file", func(t *testing.T) {
		if got := pythonPyprojectReqs(t.TempDir()); got != nil {
			t.Fatalf("pythonPyprojectReqs() = %v, want nil", got)
		}
	})
	t.Run("no project table", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "pyproject.toml", "[tool.poetry]\nrequires-python = \">=3.11\"\n")
		if got := pythonPyprojectReqs(dir); got != nil {
			t.Fatalf("pythonPyprojectReqs() = %v, want nil", got)
		}
	})
	t.Run("no requires-python key", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "pyproject.toml", "[project]\nname = \"app\"\n")
		if got := pythonPyprojectReqs(dir); got != nil {
			t.Fatalf("pythonPyprojectReqs() = %v, want nil", got)
		}
	})
	t.Run("empty value", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "pyproject.toml", "[project]\nname = \"app\"\nrequires-python = \"\"\n")
		if got := pythonPyprojectReqs(dir); got != nil {
			t.Fatalf("pythonPyprojectReqs() = %v, want nil", got)
		}
	})
	t.Run("unquoted value", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "pyproject.toml", "[project]\nname = \"app\"\nrequires-python = >=3.11\n")
		if got := pythonPyprojectReqs(dir); got != nil {
			t.Fatalf("pythonPyprojectReqs() = %v, want nil", got)
		}
	})
	t.Run("requires-python outside project table", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "pyproject.toml", "[project.urls]\nrequires-python = \">=3.11\"\n")
		if got := pythonPyprojectReqs(dir); got != nil {
			t.Fatalf("pythonPyprojectReqs() = %v, want nil", got)
		}
	})
}

func TestExtraToolVersionAliases(t *testing.T) {
	want := map[string]string{
		"java":    "java",
		"jdk":     "java",
		"openjdk": "java",
		"php":     "php",
		"ruby":    "ruby",
		"rust":    "rust",
	}
	if got := extraToolVersionAliases(); !reflect.DeepEqual(got, want) {
		t.Fatalf("extraToolVersionAliases() = %v, want %v", got, want)
	}
}
