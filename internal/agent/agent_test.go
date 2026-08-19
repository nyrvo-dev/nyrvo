package agent

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestLookupAndNames(t *testing.T) {
	wantNames := []string{"claude", "codex", "opencode"}
	if got := Names(); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("Names() = %q, want %q", got, wantNames)
	}
	if !slices.IsSorted(Names()) {
		t.Fatalf("Names() = %q, want sorted names", Names())
	}
	for _, name := range wantNames {
		a, ok := Lookup(name)
		if !ok {
			t.Fatalf("Lookup(%q) did not find a known agent", name)
		}
		if a.Name() != name {
			t.Errorf("Lookup(%q).Name() = %q", name, a.Name())
		}
	}
	if _, ok := Lookup("unknown"); ok {
		t.Fatal("Lookup(unknown) found an unknown agent")
	}
}

func TestCommand(t *testing.T) {
	tests := []struct {
		name string
		want []string
	}{
		{name: "claude", want: []string{"claude", "-p", "explain this"}},
		{name: "codex", want: []string{"codex", "exec", "--skip-git-repo-check", "explain this"}},
		{name: "opencode", want: []string{"opencode", "run", "explain this"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := Lookup(tt.name)
			if got := a.Command("explain this"); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Command() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAnalyzeExecutesDisclosedCommand(t *testing.T) {
	a, _ := Lookup("codex")
	want := a.Command("analyze")
	withExecutor(t, func(_ context.Context, argv []string, _ string, _ io.Reader) (string, string, error) {
		if !reflect.DeepEqual(argv, want) {
			t.Fatalf("executed argv = %q, disclosed argv = %q", argv, want)
		}
		return "answer", "", nil
	})
	if _, err := a.Analyze(context.Background(), "analyze"); err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
}

func TestAnalyzeStripsTerminalFormatting(t *testing.T) {
	a, _ := Lookup("opencode")
	withExecutor(t, func(_ context.Context, _ []string, _ string, _ io.Reader) (string, string, error) {
		return "\x1b[1mThe answer\x1b[0m\r\n", "", nil
	})
	got, err := a.Analyze(context.Background(), "analyze")
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	// Control bytes are never part of an answer; a --json document carrying
	// them would be a terminal artifact in a machine-readable field.
	if got != "The answer" {
		t.Fatalf("Analyze() = %q, want %q", got, "The answer")
	}
}

func TestAnalyzePassesPromptAsOneArgument(t *testing.T) {
	a, _ := Lookup("claude")
	prompt := "$(id) `whoami`;rm -rf /\nsecond line"
	withExecutor(t, func(_ context.Context, argv []string, _ string, _ io.Reader) (string, string, error) {
		if len(argv) != 3 {
			t.Fatalf("argv = %q, want exactly three elements", argv)
		}
		if argv[2] != prompt {
			t.Fatalf("prompt argv = %q, want %q", argv[2], prompt)
		}
		return "safe", "", nil
	})
	if _, err := a.Analyze(context.Background(), prompt); err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
}

func TestAnalyzeRejectsEmptyOutput(t *testing.T) {
	a, _ := Lookup("claude")
	withExecutor(t, func(_ context.Context, _ []string, _ string, _ io.Reader) (string, string, error) {
		return " \n\x1b[0m", "", nil
	})
	_, err := a.Analyze(context.Background(), "analyze")
	if err == nil || !strings.Contains(err.Error(), "empty analysis") {
		t.Fatalf("Analyze() error = %v, want empty analysis error", err)
	}
}

func TestAnalyzeReportsMissingBinary(t *testing.T) {
	a, _ := Lookup("claude")
	withExecutor(t, func(_ context.Context, _ []string, _ string, _ io.Reader) (string, string, error) {
		return "", "", exec.ErrNotFound
	})
	_, err := a.Analyze(context.Background(), "analyze")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Analyze() error = %v, want ErrUnavailable", err)
	}
}

func TestAnalyzeReportsFailureWithFirstStderrLine(t *testing.T) {
	a, _ := Lookup("codex")
	withExecutor(t, func(_ context.Context, _ []string, _ string, _ io.Reader) (string, string, error) {
		return "", "first failure\nmore detail", errors.New("exit status 1")
	})
	_, err := a.Analyze(context.Background(), "analyze")
	if err == nil {
		t.Fatal("Analyze() error = nil, want failure")
	}
	if !strings.Contains(err.Error(), "codex") || !strings.Contains(err.Error(), "first failure") {
		t.Fatalf("Analyze() error = %q, want agent name and first stderr line", err)
	}
	if strings.Contains(err.Error(), "more detail") {
		t.Fatalf("Analyze() error = %q, want only first stderr line", err)
	}
	if errors.Is(err, ErrUnavailable) {
		t.Fatalf("Analyze() error = %v, must not be ErrUnavailable", err)
	}
}

func TestAnalyzeUsesAndRemovesEmptyWorkingDirectory(t *testing.T) {
	a, _ := Lookup("opencode")
	var dir string
	withExecutor(t, func(_ context.Context, _ []string, gotDir string, _ io.Reader) (string, string, error) {
		dir = gotDir
		info, err := os.Stat(gotDir)
		if err != nil {
			t.Fatalf("working directory does not exist: %v", err)
		}
		if !info.IsDir() {
			t.Fatalf("working path %q is not a directory", gotDir)
		}
		entries, err := os.ReadDir(gotDir)
		if err != nil {
			t.Fatalf("ReadDir(%q): %v", gotDir, err)
		}
		if len(entries) != 0 {
			t.Fatalf("working directory contains %d entries, want empty", len(entries))
		}
		return "answer", "", nil
	})
	if _, err := a.Analyze(context.Background(), "analyze"); err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("working directory remains after Analyze: %v", err)
	}
}

func withExecutor(t *testing.T, fake executeFunc) {
	t.Helper()
	original := execute
	execute = fake
	t.Cleanup(func() { execute = original })
}

func TestAnalyzeKeepsEveryLineOfTheAnswer(t *testing.T) {
	a, _ := Lookup("opencode")
	withExecutor(t, func(_ context.Context, _ []string, _ string, _ io.Reader) (string, string, error) {
		// An answer may legitimately open with a blockquote, which is exactly
		// what an agent's banner line looks like. Deleting one deletes the other.
		return "\x1b[32m> the strongest candidate is the node mismatch\x1b[0m\nbecause package.json requires >=24\n", "", nil
	})
	got, err := a.Analyze(context.Background(), "analyze")
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	want := "> the strongest candidate is the node mismatch\nbecause package.json requires >=24"
	if got != want {
		t.Fatalf("Analyze() = %q, want %q", got, want)
	}
}

func TestCommandLongPromptUsesStdinOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("stdin prompt fallback applies only on Windows")
	}
	a, _ := Lookup("claude")
	prompt := strings.Repeat("x", maxPromptArgLen+1)
	if got := a.Command(prompt); !reflect.DeepEqual(got, []string{"claude", "-p", stdinPromptArg}) {
		t.Fatalf("Command() = %q, want stdin placeholder on Windows", got)
	}
}

func TestAnalyzePassesLongPromptOnStdinOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("stdin prompt fallback applies only on Windows")
	}
	a, _ := Lookup("claude")
	prompt := strings.Repeat("y", maxPromptArgLen+1)
	withExecutor(t, func(_ context.Context, argv []string, _ string, stdin io.Reader) (string, string, error) {
		if !reflect.DeepEqual(argv, []string{"claude", "-p", stdinPromptArg}) {
			t.Fatalf("argv = %q, want stdin placeholder", argv)
		}
		body, err := io.ReadAll(stdin)
		if err != nil {
			t.Fatalf("ReadAll(stdin): %v", err)
		}
		if string(body) != prompt {
			t.Fatalf("stdin = %q, want full prompt", body)
		}
		return "ok", "", nil
	})
	if _, err := a.Analyze(context.Background(), prompt); err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
}
