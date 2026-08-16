package output

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
)

// AIAgentDisclosure makes the agent boundary explicit before control passes to
// a separately installed program whose configured service Nyrvo cannot inspect.
func AIAgentDisclosure(w io.Writer, agentName string, command []string) error {
	var b strings.Builder
	b.WriteString("\nAI AGENT EXECUTION\n\n")
	b.WriteString("DISCLOSURE\n")
	fmt.Fprintf(&b, "  Agent\t%s, your own installed CLI, will be used.\n", agentName)
	b.WriteString("  Data\tThe analysis request Nyrvo built: two environment snapshots, what differs, and Nyrvo's own findings.\n")
	b.WriteString("  Privacy\tNo environment variable values are included, and home directory prefixes are replaced with ~.\n")
	b.WriteString("  Execution\tNyrvo will run the CLI program on this machine. The program will contact whatever service it is configured to use; Nyrvo does not know which service or where it runs.\n")
	fmt.Fprintf(&b, "  Command\t%s\n", displayedAgentCommand(command))
	return writeAligned(w, b.String())
}

// AIAnalysisText preserves the model's response bytes so presentation cannot
// subtly turn generated claims into Nyrvo-authored evidence.
func AIAnalysisText(w io.Writer, agentName string, analysis string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "\nAI ANALYSIS — %s\n\n", agentName)
	b.WriteString("What follows is model inference, not observation. Nyrvo's findings above are the checkable part.\n\n")
	if analysis == "" {
		b.WriteString("The agent returned no analysis.\n")
		_, err := io.WriteString(w, b.String())
		return err
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return err
	}
	if _, err := io.WriteString(w, analysis); err != nil {
		return err
	}
	// Terminating the last line is not editing the answer: without it the shell
	// prompt lands mid-sentence, and an agent that ends without a newline is
	// common enough that the alternative is a ragged report most of the time.
	if strings.HasSuffix(analysis, "\n") {
		return nil
	}
	_, err := io.WriteString(w, "\n")
	return err
}

func displayedAgentCommand(command []string) string {
	if len(command) == 0 {
		return "No command was provided."
	}

	args := make([]string, 0, len(command))
	for _, arg := range command[:len(command)-1] {
		args = append(args, displayCommandArg(arg))
	}
	args = append(args, fmt.Sprintf("<analysis request elided; final argument, %d bytes>", len(command[len(command)-1])))
	return strings.Join(args, " ")
}

func displayCommandArg(arg string) string {
	if arg != "" && strings.IndexFunc(arg, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("'\"\\`$&|;<>*?()[]{}!", r)
	}) == -1 {
		return arg
	}
	return strconv.Quote(arg)
}
