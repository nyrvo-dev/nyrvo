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
