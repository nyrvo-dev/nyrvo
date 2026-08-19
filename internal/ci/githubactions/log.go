package githubactions

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
	"github.com/nyrvo-dev/nyrvo/internal/textsafe"
)

// JobLog is what Nyrvo could safely learn from one job's log.
//
// A log is untrusted terminal output (see docs/adr/0011): it echoes step
// configuration including environment variable values, and it carries ANSI
// escape sequences. Everything here has been parsed out of sanitized lines, so
// no field can carry a control sequence, a timestamp, or an env value that the
// log's own structure (the group markers) decided to keep out.
type JobLog struct {
	RunnerVersion string
	Image         Image
	Runtimes      []snapshot.Runtime
	// Errors are the ##[error] messages in order, with the marker stripped,
	// capped at errorLimit. DroppedErrors reports how many were left out so a
	// truncated prefix is never mistaken for the whole story.
	Errors []string
	// DroppedErrors is how many ##[error] lines were not kept because
	// errorLimit was reached.
	DroppedErrors int
	// FailureLines is a bounded excerpt of the output around the failure: the
	// qualifying lines just before the first error, plus the error lines.
	FailureLines []string
}

// Image is the runner image a job executed on.
//
// It is assembled from whichever header the runner wrote: a container job gets
// one "VM Image" group with the architecture included, a standard hosted runner
// gets "Operating System" and "Runner Image" groups and states no architecture.
// A field the log did not provide stays empty.
type Image struct {
	OS      string // "linux", "darwin", "windows"
	Arch    string // "amd64", "arm64", or empty when the log did not say
	Name    string // "ubuntu:24.04" or "ubuntu-24.04"
	Version string
}

// timestampPrefix matches the runner's per-line timestamp and the single space
// after it. A line without one (multiline tool output) is kept as-is.
var timestampPrefix = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+Z `)

// imageOSRe matches "- OS: Linux (x64)" and "- OS: macOS (arm64)".
var imageOSRe = regexp.MustCompile(`^- OS: ([^ (]+) \(([^)]+)\)$`)

// excerptLimit bounds the failure excerpt so a runaway log cannot become a
// snapshot.
const excerptLimit = 20

// lineLimit caps a single line; longer lines are cut and marked with an
// ellipsis. The bound exists for the same reason as excerptLimit.
const lineLimit = 500

// errorLimit caps how many ##[error] lines a job log may contribute to the
// failure excerpt. excerptLimit bounds the lines before the failure, but error
// lines were appended without bound: a step that prints one error per output
// line turns a 10MB log into a snapshot-sized list of "Log:" notes, which is
// how a 10MB log became an 8MB snapshot. The first errors are the ones that
// identify the failure, so a prefix is kept and the dropped count is reported
// rather than hidden.
const errorLimit = 50

// parsedLogLine is one sanitized log line together with the state that decides
// whether it may be quoted.
type parsedLogLine struct {
	text     string
	inGroup  bool // inside a ##[group] ... ##[endgroup] block
	boundary bool // a ##[group] or ##[endgroup] marker line
	isError  bool // a ##[error] line; text holds the stripped message
}

// ParseJobLog reads one job's log and returns what Nyrvo could safely learn
// from it. It never returns an error: a log the parser cannot read is a gap in
// what the import knows, not a failure of the import, and an empty result is
// the honest answer to an unparseable input.
func ParseJobLog(raw []byte) *JobLog {
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})

	jl := &JobLog{}
	runtimes := make(map[string]string)
	lines := make([]parsedLogLine, 0)
	group := ""
	inGroup := false

	for _, rawLine := range strings.Split(string(raw), "\n") {
		text := sanitizeLogLine(rawLine)
		p := parsedLogLine{text: text}
		switch {
		case strings.HasPrefix(text, "##[group]"):
			group = strings.TrimPrefix(text, "##[group]")
			inGroup = true
			p.boundary = true
		case strings.HasPrefix(text, "##[endgroup]"):
			group = ""
			inGroup = false
			p.boundary = true
		case strings.HasPrefix(text, "##[error]"):
			p.isError = true
			p.text = strings.TrimPrefix(text, "##[error]")
			if len(jl.Errors) < errorLimit {
				jl.Errors = append(jl.Errors, truncateLine(p.text))
			} else {
				jl.DroppedErrors++
			}
		default:
			observeRunnerVersion(jl, text)
			observeImage(jl, group, text)
			observeRuntime(runtimes, group, text)
		}
		p.inGroup = inGroup
		lines = append(lines, p)
	}

	// The setup line is the authoritative signal for go; the "go version
	// go1.26.6 linux/amd64" line is only consulted when no setup line appeared.
	if _, ok := runtimes["go"]; !ok {
		for _, p := range lines {
			observeGoFallback(runtimes, p.text)
			if _, ok := runtimes["go"]; ok {
				break
			}
		}
	}

	jl.Runtimes = sortedRuntimes(runtimes)
	jl.FailureLines = failureExcerpt(lines, jl.Errors)
	return jl
}

// sanitizeLogLine makes one line safe to store: the leading timestamp and its
// space are removed, ANSI escape sequences and other control bytes are
// stripped, and a trailing carriage return is dropped. Everything left is kept
// verbatim.
func sanitizeLogLine(line string) string {
	line = timestampPrefix.ReplaceAllString(line, "")
	line = textsafe.Strip(line)
	return strings.TrimSuffix(line, "\r")
}

// failureExcerpt picks the output lines just before the first error, walking
// backward so it naturally stops at the step's configuration block (a group
// boundary) — the env: echo the excerpt must never quote. Error lines are
// always included, regardless of where they appear.
func failureExcerpt(lines []parsedLogLine, errors []string) []string {
	firstErr := -1
	for i, p := range lines {
		if p.isError {
			firstErr = i
			break
		}
	}

	var excerpt []string
	if firstErr >= 0 {
		for i := firstErr - 1; i >= 0 && len(excerpt) < excerptLimit; i-- {
			p := lines[i]
			if p.boundary {
				break
			}
			if p.inGroup || p.isError {
				continue
			}
			if strings.TrimSpace(p.text) == "" {
				continue
			}
			excerpt = append(excerpt, truncateLine(p.text))
		}
		slices.Reverse(excerpt)
	}

	out := make([]string, 0, len(excerpt)+len(errors))
	out = append(out, excerpt...)
	out = append(out, errors...)
	return out
}

// truncateLine caps a single line on runes, appending an ellipsis when it cut
// something, so a runaway log line cannot become a snapshot.
func truncateLine(s string) string {
	if utf8.RuneCountInString(s) <= lineLimit {
		return s
	}
	return string([]rune(s)[:lineLimit]) + "…"
}

// observeRunnerVersion reads the runner's version from its announcement line.
func observeRunnerVersion(jl *JobLog, line string) {
	if jl.RunnerVersion != "" {
		return
	}
	const prefix = "Current runner version: '"
	if !strings.HasPrefix(line, prefix) {
		return
	}
	if v, _, ok := strings.Cut(strings.TrimPrefix(line, prefix), "'"); ok {
		jl.RunnerVersion = v
	}
}

// observeImage fills the image from the "VM Image" group. An unrecognized OS is
// left unknown rather than guessed.
func observeImage(jl *JobLog, group, line string) {
	// Runners write their header two different ways. A job in a container gets
	// a "VM Image" group carrying OS, architecture and image name together; a
	// standard hosted runner instead gets separate "Operating System" and
	// "Runner Image" groups, and states no architecture at all. Both are real —
	// each was found in a recorded log — so both are read, and the fields a
	// given format does not carry simply stay empty.
	switch group {
	case "Operating System":
		observeOperatingSystemGroup(jl, line)
		return
	case "Runner Image":
		observeRunnerImageGroup(jl, line)
		return
	case "VM Image":
	default:
		return
	}
	if m := imageOSRe.FindStringSubmatch(line); m != nil {
		switch m[1] {
		case "Linux":
			jl.Image.OS = "linux"
		case "macOS":
			jl.Image.OS = "darwin"
		case "Windows":
			jl.Image.OS = "windows"
		default:
			return
		}
		switch m[2] {
		case "x64":
			jl.Image.Arch = "amd64"
		case "arm64":
			jl.Image.Arch = "arm64"
		}
		return
	}
	if v, ok := strings.CutPrefix(line, "- Name: "); ok {
		jl.Image.Name = v
		return
	}
	if v, ok := strings.CutPrefix(line, "- Version: "); ok {
		jl.Image.Version = v
	}
}

// observeRuntime reads the installed runtime versions from the lines the
// setup actions print. Node's line is only read inside the "Environment
// details" group; go and python lines are matched anywhere.
func observeRuntime(runtimes map[string]string, group, line string) {
	switch {
	case strings.HasPrefix(line, "Successfully set up Go version "):
		recordRuntime(runtimes, "go", strings.TrimPrefix(line, "Successfully set up Go version "))
	case group == "Environment details" && strings.HasPrefix(line, "node: v"):
		recordRuntime(runtimes, "node", strings.TrimPrefix(line, "node: v"))
	case strings.HasPrefix(line, "Successfully set up CPython (") && strings.HasSuffix(line, ")"):
		v := strings.TrimSuffix(strings.TrimPrefix(line, "Successfully set up CPython ("), ")")
		recordRuntime(runtimes, "python", v)
	case strings.HasPrefix(line, "Installed Java version: "):
		v := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(line, "Installed Java version: ")), ".")
		recordRuntime(runtimes, "java", v)
	case group == "Environment details" && strings.HasPrefix(line, "java: "):
		recordRuntime(runtimes, "java", strings.TrimSpace(strings.TrimPrefix(line, "java: ")))
	case strings.HasPrefix(line, "Using Ruby version: "):
		recordRuntime(runtimes, "ruby", strings.TrimPrefix(line, "Using Ruby version: "))
	case group == "Environment details" && strings.HasPrefix(line, "ruby: "):
		recordRuntime(runtimes, "ruby", strings.TrimSpace(strings.TrimPrefix(line, "ruby: ")))
	case strings.HasPrefix(line, "PHP "):
		rest := strings.TrimSpace(strings.TrimPrefix(line, "PHP "))
		if field, _, _ := strings.Cut(rest, " "); field != "" {
			recordRuntime(runtimes, "php", field)
		}
	case strings.HasPrefix(line, "Installed .NET SDK version "):
		recordRuntime(runtimes, "dotnet", strings.TrimSpace(strings.TrimPrefix(line, "Installed .NET SDK version ")))
	case strings.HasPrefix(line, "Installed pnpm version "):
		recordRuntime(runtimes, "pnpm", strings.TrimSpace(strings.TrimPrefix(line, "Installed pnpm version ")))
	case strings.Contains(line, "info: syncing channel updates for '"):
		const prefix = "info: syncing channel updates for '"
		start := strings.Index(line, prefix) + len(prefix)
		if end := strings.Index(line[start:], "'"); end >= 0 {
			channel := line[start : start+end]
			v, _, _ := strings.Cut(channel, "-")
			recordRuntime(runtimes, "rust", v)
		}
	}
}

// observeGoFallback reads go's version from the "go version go1.26.6 ..."
// line, used only when no setup line was seen.
func observeGoFallback(runtimes map[string]string, line string) {
	if _, ok := runtimes["go"]; ok {
		return
	}
	const prefix = "go version go"
	if !strings.HasPrefix(line, prefix) {
		return
	}
	if v, _, ok := strings.Cut(strings.TrimPrefix(line, prefix), " "); ok {
		recordRuntime(runtimes, "go", v)
	}
}

// recordRuntime keeps the first occurrence of a runtime, skipping versions that
// are not dotted digits: anything else is not a version Nyrvo can compare.
func recordRuntime(runtimes map[string]string, name, version string) {
	if _, ok := runtimes[name]; ok {
		return
	}
	if !bareVersion.MatchString(version) {
		return
	}
	runtimes[name] = version
}

// sortedRuntimes flattens the collected runtimes into the snapshot's slice
// form, ordered by name so two parses of one log are byte-identical.
func sortedRuntimes(m map[string]string) []snapshot.Runtime {
	out := make([]snapshot.Runtime, 0, len(m))
	for name, version := range m {
		out = append(out, snapshot.Runtime{Name: name, Version: version})
	}
	slices.SortFunc(out, func(a, b snapshot.Runtime) int {
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// ApplyJobLog folds what a log revealed into a run snapshot.
//
// This is where an imported run stops guessing. Run metadata reports no runtime
// versions at all and cannot name the platform behind a self-hosted label; the
// log observed both. Log-derived facts therefore take precedence over the
// nothing that preceded them, but never overwrite something already observed.
func ApplyJobLog(snap *snapshot.Snapshot, jl *JobLog) {
	if snap == nil || jl == nil {
		return
	}

	// Versions read from the log are observations of what the runner actually
	// installed, which is exactly the gap the run's metadata leaves open. Once
	// they are known the metadata caveat is no longer true, so it goes.
	if len(jl.Runtimes) > 0 {
		snap.Runtimes = append(snap.Runtimes, jl.Runtimes...)
		if snap.Source != nil {
			snap.Source.Notes = slices.DeleteFunc(snap.Source.Notes, func(n string) bool {
				return n == runtimesFromLogNote
			})
		}
	}

	// The image names the platform even when the runner label did not: a
	// self-hosted label tells us nothing, but "- OS: Linux (x64)" is an
	// observation. It only fills a gap; a platform already derived from a known
	// hosted runner label is not second-guessed.
	if snap.System == nil && jl.Image.OS != "" {
		snap.System = &snapshot.System{OS: jl.Image.OS, Arch: jl.Image.Arch}
	}

	if snap.Source == nil {
		snap.Source = &snapshot.Source{Kind: snapshot.SourceGitHubActionsRun}
	}
	notes := snap.Source.Notes
	if jl.Image.Name != "" {
		note := "The job ran on runner image " + jl.Image.Name
		if jl.Image.Version != "" {
			note += " version " + jl.Image.Version
		}
		notes = append(notes, note+".")
	}
	if jl.RunnerVersion != "" {
		notes = append(notes, "The runner was version "+jl.RunnerVersion+".")
	}
	// The failure excerpt is the point of reading the log at all. ParseJobLog
	// bounded it to excerptLimit lines before the failure plus errorLimit error
	// lines, and kept it stripped of group contents, so the env: block the
	// runner echoes before each step cannot appear here (docs/adr/0011). A
	// bound that silently kept a prefix would overstate the evidence, so a
	// truncation that dropped error lines is reported.
	for _, line := range jl.FailureLines {
		notes = append(notes, "Log: "+line)
	}
	if jl.DroppedErrors > 0 {
		notes = append(notes, fmt.Sprintf("The log showed %d more ##[error] lines; only the first %d were kept.", jl.DroppedErrors, errorLimit))
	}
	// The log lines were sanitized when parsed, so this is defense in depth:
	// the invariant is that no Source.Notes entry ever carries a control byte.
	snap.Source.Notes = textsafe.StripAll(notes)

	snap.Normalize()
}

// observeOperatingSystemGroup reads the "Operating System" group a standard
// hosted runner writes: the distribution name on its own line ("Ubuntu",
// "macOS", "Microsoft Windows Server 2022"). It names the OS but never the
// architecture, which stays empty rather than assumed.
func observeOperatingSystemGroup(jl *JobLog, line string) {
	if jl.Image.OS != "" {
		return
	}
	switch {
	case strings.HasPrefix(line, "Ubuntu"), strings.HasPrefix(line, "Debian"):
		jl.Image.OS = "linux"
	case strings.HasPrefix(line, "macOS"), strings.HasPrefix(line, "Mac OS"):
		jl.Image.OS = "darwin"
	case strings.Contains(line, "Windows"):
		jl.Image.OS = "windows"
	}
}

// observeRunnerImageGroup reads the "Runner Image" group of a standard hosted
// runner, which names the image and its build.
func observeRunnerImageGroup(jl *JobLog, line string) {
	if v, ok := strings.CutPrefix(line, "Image: "); ok {
		jl.Image.Name = v
		return
	}
	if v, ok := strings.CutPrefix(line, "Version: "); ok && jl.Image.Version == "" {
		jl.Image.Version = v
	}
}
