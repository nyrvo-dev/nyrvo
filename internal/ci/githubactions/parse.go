package githubactions

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseFile reads and parses one workflow file, setting the returned
// workflow's Path to the file it came from. A file that parses but declares no
// jobs is not an error: it is simply a workflow with nothing to observe.
func ParseFile(path string) (*Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	w := &Workflow{Path: path}
	if raw == nil {
		// An empty file is a workflow that declares nothing, which is legal.
		return w, nil
	}

	w.Name, _ = scalarString(raw["name"])

	// These workflow-level keys change when or with what permissions the whole
	// workflow runs; Nyrvo reads configuration, so it records their presence
	// without guessing at their meaning.
	if _, ok := raw["on"]; ok {
		w.Notes = append(w.Notes, "on triggers are not modelled")
	}
	if _, ok := raw["permissions"]; ok {
		w.Notes = append(w.Notes, "permissions are not modelled")
	}
	if _, ok := raw["concurrency"]; ok {
		w.Notes = append(w.Notes, "concurrency is not modelled")
	}
	if _, ok := raw["defaults"]; ok {
		w.Notes = append(w.Notes, "defaults are not modelled")
	}

	jobsRaw, ok := raw["jobs"]
	if !ok {
		return w, nil
	}
	jobs, ok := jobsRaw.(map[string]any)
	if !ok {
		w.Notes = append(w.Notes, "jobs is not a mapping; no jobs modelled")
		return w, nil
	}

	for _, id := range sortedKeys(jobs) {
		if raw, ok := jobs[id].(map[string]any); ok {
			w.Jobs = append(w.Jobs, parseJob(id, raw))
		} else {
			// A job that is not a mapping cannot be described; the file still
			// parsed, so this is a note rather than a failure.
			w.Notes = append(w.Notes, jobNote(id, "is not a mapping; not modelled"))
		}
	}
	return w, nil
}

// ParseDir parses every *.yml and *.yaml file directly inside dir, returning
// the workflows sorted by path. A missing directory is a repository without a
// workflow directory, which is a normal state rather than an error; a file
// that fails to parse aborts the call so the user hears exactly which file is
// broken.
func ParseDir(dir string) ([]*Workflow, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext == ".yml" || ext == ".yaml" {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(paths)

	workflows := make([]*Workflow, 0, len(paths))
	for _, p := range paths {
		w, err := ParseFile(p)
		if err != nil {
			return nil, err
		}
		workflows = append(workflows, w)
	}
	return workflows, nil
}

// parseJob fills one job from its decoded mapping. Constructs Nyrvo sees but
// does not model are recorded on the job's Notes rather than guessed at: the
// parser describes what a job declares, never what it might mean.
func parseJob(id string, raw map[string]any) Job {
	j := Job{ID: id}
	j.Name, _ = scalarString(raw["name"])

	// runs-on: a scalar names one runner, a list names a label set. Values stay
	// verbatim: an expression like ${{ matrix.os }} is preserved rather than
	// resolved, because guessing a platform Nyrvo cannot observe would describe
	// an environment that never existed. The group:/labels: object form is not
	// modelled.
	switch r := raw["runs-on"].(type) {
	case []any:
		for _, e := range r {
			if s, ok := scalarString(e); ok {
				j.RunsOn = append(j.RunsOn, s)
			} else {
				j.Notes = append(j.Notes, jobNote(id, "runs-on list has a non-scalar entry; it is not modelled"))
			}
		}
	case map[string]any:
		j.Notes = append(j.Notes, jobNote(id, "runs-on group/labels form is not modelled"))
	default:
		if s, ok := scalarString(raw["runs-on"]); ok {
			j.RunsOn = []string{s}
		}
	}

	// container: the scalar form names an image directly; the object form
	// carries it under "image". The image is the part that replaces the runner
	// OS, so it outranks runs-on when deciding where the job runs.
	switch c := raw["container"].(type) {
	case map[string]any:
		if img, ok := scalarString(c["image"]); ok {
			j.Container = img
		} else {
			j.Notes = append(j.Notes, jobNote(id, "container has no image; not modelled"))
		}
	default:
		if s, ok := scalarString(raw["container"]); ok {
			j.Container = s
		}
	}

	if svc, ok := raw["services"].(map[string]any); ok {
		for _, sid := range sortedKeys(svc) {
			s := Service{ID: sid}
			if m, ok := svc[sid].(map[string]any); ok {
				s.Image, _ = scalarString(m["image"])
				s.Env = stringMap(m["env"], &j.Notes, jobNote(id, "service "+strconv.Quote(sid)+" env has a non-scalar entry; it is not modelled"))
			} else {
				j.Notes = append(j.Notes, jobNote(id, "service "+strconv.Quote(sid)+" is not a mapping; not modelled"))
			}
			j.Services = append(j.Services, s)
		}
	}

	j.Env = stringMap(raw["env"], &j.Notes, jobNote(id, "env has a non-scalar entry; it is not modelled"))

	if steps, ok := raw["steps"].([]any); ok {
		for _, sv := range steps {
			j.Steps = append(j.Steps, parseStep(id, sv, &j.Notes))
		}
	} else if _, ok := raw["steps"]; ok {
		j.Notes = append(j.Notes, jobNote(id, "steps is not a sequence; not modelled"))
	}

	if strategy, ok := raw["strategy"].(map[string]any); ok {
		parseMatrix(id, strategy["matrix"], &j)
	}

	// Job-level keys that change what or when the job runs are recognized but
	// not modelled; each is noted where it appears.
	if _, ok := raw["uses"]; ok {
		j.Notes = append(j.Notes, jobNote(id, "reusable workflow uses is not modelled"))
	}
	if _, ok := raw["if"]; ok {
		j.Notes = append(j.Notes, jobNote(id, "if conditions are not modelled"))
	}
	if _, ok := raw["permissions"]; ok {
		j.Notes = append(j.Notes, jobNote(id, "permissions are not modelled"))
	}
	if _, ok := raw["concurrency"]; ok {
		j.Notes = append(j.Notes, jobNote(id, "concurrency is not modelled"))
	}
	if _, ok := raw["defaults"]; ok {
		j.Notes = append(j.Notes, jobNote(id, "defaults are not modelled"))
	}

	return j
}

// parseStep fills one step. Step if conditions are the only recognized
// construct left unmodelled here; the other fields are stored verbatim because
// they describe what the step declares, which is all a configuration reader
// can truthfully claim.
func parseStep(jobID string, v any, notes *[]string) Step {
	s := Step{}
	m, ok := v.(map[string]any)
	if !ok {
		return s
	}
	s.Name, _ = scalarString(m["name"])
	s.Uses, _ = scalarString(m["uses"])
	s.Run, _ = scalarString(m["run"])
	s.WorkingDirectory, _ = scalarString(m["working-directory"])
	s.With = stringMap(m["with"], notes, jobNote(jobID, "step "+strconv.Quote(s.Name)+" with has a non-scalar entry; it is not modelled"))
	s.Env = stringMap(m["env"], notes, jobNote(jobID, "step "+strconv.Quote(s.Name)+" env has a non-scalar entry; it is not modelled"))
	if _, ok := m["if"]; ok {
		*notes = append(*notes, jobNote(jobID, "step "+strconv.Quote(s.Name)+" if conditions are not modelled"))
	}
	return s
}

// parseMatrix records the parts of a strategy matrix that describe concrete
// environments and notes the rest. include and exclude rewrite the expansion —
// include can add combinations the scalar keys never produce — so either one
// voids the whole matrix rather than leaving a subset that lies.
func parseMatrix(id string, m any, j *Job) {
	matrix, ok := m.(map[string]any)
	if !ok {
		if m != nil {
			j.Notes = append(j.Notes, jobNote(id, "strategy.matrix is not a mapping; not modelled"))
		}
		return
	}
	if _, ok := matrix["include"]; ok {
		j.Notes = append(j.Notes, jobNote(id, "strategy.matrix.include is not modelled"))
		return
	}
	if _, ok := matrix["exclude"]; ok {
		j.Notes = append(j.Notes, jobNote(id, "strategy.matrix.exclude is not modelled"))
		return
	}
	for _, key := range sortedKeys(matrix) {
		list, ok := matrix[key].([]any)
		if !ok {
			j.Notes = append(j.Notes, jobNote(id, "strategy.matrix."+key+" is not modelled"))
			continue
		}
		vals, ok := scalarList(list)
		if !ok {
			j.Notes = append(j.Notes, jobNote(id, "strategy.matrix."+key+" is not modelled"))
			continue
		}
		if j.Matrix == nil {
			j.Matrix = make(map[string][]string)
		}
		j.Matrix[key] = vals
	}
}

// scalarList converts a sequence of scalar values to their string form. A
// value that is not a scalar, or a string carrying an expression, fails the
// whole list: the matrix would then describe combinations that never ran.
func scalarList(list []any) ([]string, bool) {
	vals := make([]string, 0, len(list))
	for _, e := range list {
		s, ok := scalarString(e)
		if !ok {
			return nil, false
		}
		if strings.Contains(s, "${{") {
			return nil, false
		}
		vals = append(vals, s)
	}
	return vals, true
}

// stringMap keeps the scalar entries of a decoded mapping. Non-scalar values
// cannot be reproduced faithfully as text, so they are dropped: an env block
// is a list of literal assignments, and the literals are the only part Nyrvo
// claims to understand. Skipping them is not silence — ADR 0006 requires a
// note whenever a recognized construct is left unmodelled.
func stringMap(v any, notes *[]string, skippedNote string) map[string]string {
	if v == nil {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		noteSkipped(notes, skippedNote)
		return nil
	}
	out := make(map[string]string, len(m))
	skipped := false
	for k, val := range m {
		if s, ok := scalarString(val); ok {
			out[k] = s
		} else {
			skipped = true
		}
	}
	if skipped {
		noteSkipped(notes, skippedNote)
	}
	return out
}

func noteSkipped(notes *[]string, msg string) {
	if notes == nil || msg == "" {
		return
	}
	*notes = append(*notes, msg)
}

// scalarString returns a scalar value in its literal text form. Integers,
// floats, and booleans become what YAML would print for them; strings pass
// through verbatim, which is what keeps expressions like ${{ matrix.os }}
// readable. Anything else is not a scalar and is left to the caller to note.
func scalarString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case int:
		return strconv.Itoa(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case uint64:
		return strconv.FormatUint(t, 10), true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(t), true
	default:
		return "", false
	}
}

// jobNote prefixes a note with the job it belongs to, so a note can be traced
// back to the exact job without reconstructing it from position in the file.
func jobNote(id, msg string) string {
	return "job " + strconv.Quote(id) + ": " + msg
}

// sortedKeys returns a mapping's keys in sorted order. Job and service
// declarations are maps in the file, and Go map iteration is unordered; a
// stable order keeps two captures of the same file byte-identical, which
// snapshot determinism depends on.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
