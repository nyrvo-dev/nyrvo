package output

import (
	"fmt"
	"io"
	"strings"

	"github.com/nyrvo-dev/nyrvo/internal/analysis"
)

const (
	aiRequestBegin = "----- BEGIN AI REQUEST -----"
	aiRequestEnd   = "----- END AI REQUEST -----"
)

// AIRequestText keeps the proposed model input visibly separate from Nyrvo's
// deterministic report so evidence is not mistaken for model inference.
func AIRequestText(w io.Writer, in analysis.Input, prompt string) error {
	var b strings.Builder
	// The deterministic report is printed immediately above this. A blank line
	// is what stops the two from reading as one continuous report.
	b.WriteString("\nAI ANALYSIS REQUEST (NOT EXECUTED)\n\n")
	b.WriteString("This is a request for analysis, not output from a model.\n\n")
	b.WriteString("DISCLOSURE\n")
	b.WriteString("  Agent\tNo agent was selected or run.\n")
	b.WriteString("  Data\tTwo environment snapshots, their differences, and Nyrvo's own findings.\n")
	b.WriteString("  Execution\tNothing was executed, and nothing left this machine.\n")
	b.WriteString("  Command\tNo command was invoked.\n")
	b.WriteString("  Privacy\tEnvironment variable values are never recorded, and only the names this diagnosis refers to are included.\n")
	// A second row labelled "Privacy" reads as a repeated heading rather than a
	// separate fact; the column exists to name what each line is about.
	b.WriteString("  Paths\tHome directory prefixes in paths are replaced with ~.\n")
	fmt.Fprintf(&b, "\n%s\n", aiRequestBegin)
	if err := writeAligned(w, b.String()); err != nil {
		return err
	}
	// Bypassing tabwriter preserves tabs and every other byte supplied by the caller.
	if _, err := io.WriteString(w, prompt); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\n%s\n\nPaste this request into the agent of your choice.\n", aiRequestEnd)
	return err
}

type aiRequestDoc struct {
	SchemaVersion int  `json:"schema_version"`
	Executed      bool `json:"executed"`
	// Agent and Command are absent until something actually ran, so a consumer
	// that finds them knows a program was invoked, not merely offered.
	Agent   string   `json:"agent,omitempty"`
	Command []string `json:"command,omitempty"`
	Prompt  string   `json:"prompt"`
	// Analysis is the agent's answer, verbatim. It is deliberately a plain
	// string: Nyrvo does not parse, score, or restructure model output, and a
	// consumer must not be able to mistake it for a Nyrvo finding.
	Analysis string         `json:"analysis,omitempty"`
	Input    analysis.Input `json:"input"`
}

// AIRequestJSON makes the lack of model execution explicit for consumers that
// cannot rely on human-readable disclosure text.
func AIRequestJSON(w io.Writer, in analysis.Input, prompt string) error {
	return JSON(w, aiRequestDoc{
		SchemaVersion: in.SchemaVersion,
		Executed:      false,
		Prompt:        prompt,
		Input:         in,
	})
}

// AIResultJSON is the same document after an agent ran, so a consumer reads one
// shape either way and decides on `executed` rather than on which fields exist.
func AIResultJSON(w io.Writer, in analysis.Input, prompt, agentName string, command []string, result string) error {
	return JSON(w, aiRequestDoc{
		SchemaVersion: in.SchemaVersion,
		Executed:      true,
		Agent:         agentName,
		Command:       elidePrompt(command, prompt),
		Prompt:        prompt,
		Analysis:      result,
		Input:         in,
	})
}

// promptArgument stands in for the request wherever the command line is shown.
const promptArgument = "<request>"

// elidePrompt replaces the request argument with a placeholder. The command is
// recorded so a reader can see exactly what ran; repeating a multi-kilobyte
// prompt inside it, next to the copy the document already carries, only makes
// the document harder to read for no information gained.
func elidePrompt(command []string, prompt string) []string {
	if len(command) == 0 {
		return nil
	}
	out := make([]string, len(command))
	copy(out, command)
	for i, arg := range out {
		if arg == prompt {
			out[i] = promptArgument
		}
	}
	return out
}
