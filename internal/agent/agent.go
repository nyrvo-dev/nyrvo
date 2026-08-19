// Package agent runs analysis requests through AI agent CLIs already installed
// by the user.
package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/textsafe"
)

// DefaultTimeout allows a local agent enough time to reason and respond while
// still preventing a stalled process from blocking doctor indefinitely.
const DefaultTimeout = 10 * time.Minute

// maxPromptArgLen is a conservative margin under Windows' ~8191-character
// CreateProcess limit. Prompts longer than this are passed on stdin with "-"
// as the final argument so the disclosed argv stays within the platform cap.
const maxPromptArgLen = 7000

// stdinPromptArg is the placeholder argv element when the real prompt is on stdin.
const stdinPromptArg = "-"

// ErrUnavailable lets callers distinguish installation guidance from an agent
// process that started and failed.
var ErrUnavailable = errors.New("agent unavailable")

// Agent keeps command construction and execution on the same data so the
// command disclosed to the user cannot drift from the command that runs.
type Agent struct {
	name string
	argv []string
}

// agents is a table on purpose. Supporting another CLI is one line here plus a
// verified invocation, which is a change a contributor can make in a pull
// request; an interface with one implementation per vendor, or a command line
// read from configuration, would be more machinery and — in the configuration
// case — a way to make Nyrvo run an arbitrary program.
var agents = map[string]Agent{
	"claude":   {name: "claude", argv: []string{"claude", "-p"}},
	"codex":    {name: "codex", argv: []string{"codex", "exec", "--skip-git-repo-check"}},
	"opencode": {name: "opencode", argv: []string{"opencode", "run"}},
}

type executeFunc func(ctx context.Context, argv []string, dir string, stdin io.Reader) (stdout, stderr string, err error)

var execute executeFunc = func(ctx context.Context, argv []string, dir string, stdin io.Reader) (string, string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// Lookup returns the agent Nyrvo knows by that name.
func Lookup(name string) (Agent, bool) {
	a, ok := agents[name]
	return a, ok
}

// Names lists the agents Nyrvo can invoke, sorted, so an error can tell the
// user what to choose from.
func Names() []string {
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (a Agent) Name() string { return a.name }

func (a Agent) usesStdinPrompt(prompt string) bool {
	return runtime.GOOS == "windows" && len(prompt) > maxPromptArgLen
}

// Command returns the exact argument vector Analyze will execute. It exists so
// the user can be shown what is about to run, which is only worth anything if
// the two cannot drift apart.
func (a Agent) Command(prompt string) []string {
	argv := make([]string, len(a.argv), len(a.argv)+1)
	copy(argv, a.argv)
	if a.usesStdinPrompt(prompt) {
		return append(argv, stdinPromptArg)
	}
	return append(argv, prompt)
}

// Available reports whether the CLI is installed. Nyrvo never installs it: the
// agent is the user's own tool, authenticated on their own account.
func (a Agent) Available() bool {
	if len(a.argv) == 0 {
		return false
	}
	_, err := exec.LookPath(a.argv[0])
	return err == nil
}

// Analyze hands the request to the CLI and returns its answer verbatim.
//
// The prompt is passed as one argument in a vector, never through a shell, so
// nothing inside it can be read as a command.
func (a Agent) Analyze(ctx context.Context, prompt string) (string, error) {
	argv := a.Command(prompt)
	if len(argv) < 2 {
		return "", fmt.Errorf("%s has no command", a.name)
	}

	// Agent CLIs can inspect their working tree with file tools. An empty
	// directory confines them to the evidence Nyrvo supplied in the prompt.
	dir, err := os.MkdirTemp("", "nyrvo-agent-")
	if err != nil {
		return "", fmt.Errorf("%s: create isolated working directory: %w", a.name, err)
	}
	// A directory that cannot be removed is a tidiness problem in the system
	// temporary directory, not something that changes the analysis the user
	// asked for.
	defer func() { _ = os.RemoveAll(dir) }()

	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	var stdin io.Reader
	if a.usesStdinPrompt(prompt) {
		stdin = strings.NewReader(prompt)
	}

	stdout, stderr, err := execute(ctx, argv, dir, stdin)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("%s not found: %w", a.name, ErrUnavailable)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("%s failed: %w", a.name, ctxErr)
		}
		msg := firstLine(strings.TrimSpace(stderr))
		if msg != "" {
			return "", fmt.Errorf("%s failed: %w: %s", a.name, err, msg)
		}
		return "", fmt.Errorf("%s failed: %w", a.name, err)
	}

	answer := cleanOutput(stdout)
	if answer == "" {
		return "", fmt.Errorf("%s returned an empty analysis", a.name)
	}
	return answer, nil
}

// cleanOutput removes terminal formatting and nothing else.
//
// Control sequences are never part of an answer, so dropping them loses
// nothing. A line is a different matter: opencode prefixes its answers with a
// banner naming the model, and an earlier version of this recognised and
// deleted it. That silently removes the first line of any answer that happens
// to open with a blockquote, and it trades a visible, harmless line of chrome
// for invisible data loss. The banner also says which model replied, which is
// worth showing under a heading that claims to name the agent.
func cleanOutput(output string) string {
	// The same stripper a snapshot goes through, differing only in keeping the
	// newlines an answer is written in. Keeping a second copy here is how the
	// two drifted apart: this one scanned bytes, so it mistook the tail of a
	// multi-byte character for a control introducer and swallowed the rest of
	// the line.
	return strings.TrimSpace(textsafe.StripKeepingNewlines(output))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
