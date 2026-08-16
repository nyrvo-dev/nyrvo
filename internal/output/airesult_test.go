package output

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestAIAgentDisclosureCoversExecutionBoundary(t *testing.T) {
	var buf bytes.Buffer
	if err := AIAgentDisclosure(&buf, "claude", []string{"claude", "request"}); err != nil {
		t.Fatalf("AIAgentDisclosure: %v", err)
	}
	got := buf.String()

	for _, want := range []string{
		"claude, your own installed CLI, will be used.",
		"two environment snapshots, what differs, and Nyrvo's own findings",
		"No environment variable values are included",
		"home directory prefixes are replaced with ~",
		"will run the CLI program on this machine",
		"will contact whatever service it is configured to use",
		"does not know which service or where it runs",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("disclosure missing %q:\n%s", want, got)
		}
	}
	for _, point := range []string{"Agent", "Data", "Execution", "Command"} {
		if !strings.Contains(got, "  "+point) {
			t.Errorf("disclosure missing %s point:\n%s", point, got)
		}
	}
	for _, claim := range []string{"exchange is local", "exchange is external"} {
		if strings.Contains(strings.ToLower(got), claim) {
			t.Errorf("disclosure makes unsupported claim %q:\n%s", claim, got)
		}
	}
}

func TestAIAgentDisclosureElidesOnlyPromptArgument(t *testing.T) {
	prompt := strings.Repeat("sensitive analysis request ", 200)
	command := []string{"opencode", "run", "--model", "openai/gpt-5", "--label=doctor run", prompt}
	var buf bytes.Buffer
	if err := AIAgentDisclosure(&buf, "opencode", command); err != nil {
		t.Fatalf("AIAgentDisclosure: %v", err)
	}
	got := buf.String()

	wantCommand := fmt.Sprintf("opencode run --model openai/gpt-5 \"--label=doctor run\" <analysis request elided; final argument, %d bytes>", len(prompt))
	if !strings.Contains(got, wantCommand) {
		t.Errorf("command display missing exact program, flags, or prompt placeholder %q:\n%s", wantCommand, got)
	}
	if strings.Contains(got, prompt) {
		t.Errorf("command display included the full prompt")
	}
}

func TestAIAnalysisTextNamesAgentAndPreservesBody(t *testing.T) {
	for _, analysis := range []string{
		"Likely cause:\n\tThe toolchain differs.",
		"Likely cause:\n\tThe toolchain differs.\n",
	} {
		t.Run(fmt.Sprintf("trailing_newline_%t", strings.HasSuffix(analysis, "\n")), func(t *testing.T) {
			var buf bytes.Buffer
			if err := AIAnalysisText(&buf, "codex", analysis); err != nil {
				t.Fatalf("AIAnalysisText: %v", err)
			}
			got := buf.String()

			if !strings.Contains(got, "AI ANALYSIS — codex") {
				t.Errorf("analysis heading does not name agent:\n%s", got)
			}
			if !strings.Contains(got, "model inference, not observation") || !strings.Contains(got, "findings above are the checkable part") {
				t.Errorf("analysis does not distinguish inference from findings:\n%s", got)
			}
			const separator = "findings above are the checkable part.\n\n"
			_, body, ok := strings.Cut(got, separator)
			if !ok {
				t.Fatalf("analysis body separator missing:\n%s", got)
			}
			// The body is preserved byte for byte apart from terminating the
			// last line, which is line ending rather than content: without it
			// the shell prompt lands mid-sentence.
			if strings.TrimSuffix(body, "\n") != strings.TrimSuffix(analysis, "\n") {
				t.Errorf("analysis body changed: got %q, want %q", body, analysis)
			}
			if !strings.HasSuffix(body, "differs.\n") {
				t.Errorf("analysis body is not terminated: %q", body)
			}
		})
	}
}

func TestAIAnalysisTextReportsEmptyResponse(t *testing.T) {
	var buf bytes.Buffer
	if err := AIAnalysisText(&buf, "claude", ""); err != nil {
		t.Fatalf("AIAnalysisText: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "AI ANALYSIS — claude") || !strings.Contains(got, "The agent returned no analysis.") {
		t.Errorf("empty response was presented as an unexplained blank analysis:\n%s", got)
	}
}

func TestAIAnalysisTextTerminatesTheLastLine(t *testing.T) {
	for _, analysis := range []string{"the node mismatch", "the node mismatch\n"} {
		var buf bytes.Buffer
		if err := AIAnalysisText(&buf, "claude", analysis); err != nil {
			t.Fatalf("AIAnalysisText: %v", err)
		}
		got := buf.String()
		if !strings.HasSuffix(got, "the node mismatch\n") {
			t.Errorf("AIAnalysisText(%q) did not terminate the last line: %q", analysis, got)
		}
		if strings.HasSuffix(got, "\n\n") {
			t.Errorf("AIAnalysisText(%q) added a ragged blank line: %q", analysis, got)
		}
	}
}
