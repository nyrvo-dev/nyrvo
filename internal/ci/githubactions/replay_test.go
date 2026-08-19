package githubactions

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestReplay(t *testing.T) {
	tests := []struct {
		name string
		job  *Job
		want ReplayPlan
	}{
		{
			name: "plain run step has no reason",
			job: &Job{
				ID:   "build",
				Name: "Build everything",
				Steps: []Step{
					{Name: "Test", Run: "go test ./..."},
				},
			},
			want: ReplayPlan{
				Job: "build",
				Steps: []ReplayStep{
					{Name: "Test", Command: "go test ./..."},
				},
			},
		},
		{
			name: "run step with expression keeps command verbatim",
			job: &Job{
				ID: "build",
				Steps: []Step{
					{Name: "Say", Run: `echo "hi ${{ matrix.os }}"`},
				},
			},
			want: ReplayPlan{
				Job: "build",
				Steps: []ReplayStep{
					{
						Name:    "Say",
						Command: `echo "hi ${{ matrix.os }}"`,
						Reason:  "command contains ${{ matrix.os }}, whose value is unknown outside the runner",
					},
				},
			},
		},
		{
			name: "uses step names the action",
			job: &Job{
				ID: "build",
				Steps: []Step{
					{Name: "Upload", Uses: "actions/upload-artifact@v4"},
				},
			},
			want: ReplayPlan{
				Job: "build",
				Steps: []ReplayStep{
					{
						Name:   "Upload",
						Action: "actions/upload-artifact@v4",
						Reason: "uses actions/upload-artifact@v4; Nyrvo does not fetch or run actions, so run it yourself",
					},
				},
			},
		},
		{
			name: "setup-node step names the version",
			job: &Job{
				ID: "build",
				Steps: []Step{
					{Name: "Node", Uses: "actions/setup-node@v4", With: map[string]string{"node-version": "20"}},
				},
			},
			want: ReplayPlan{
				Job: "build",
				Steps: []ReplayStep{
					{
						Name:   "Node",
						Action: "actions/setup-node@v4",
						Reason: "actions/setup-node@v4 installs node 20; install it yourself or use nvm",
					},
				},
			},
		},
		{
			name: "job env and step env merge with step override",
			job: &Job{
				ID: "test",
				Env: map[string]string{
					"CI":         "true",
					"NODE_ENV":   "production",
					"OVERRIDDEN": "job",
				},
				Steps: []Step{
					{
						Name: "Run",
						Run:  "echo hi",
						Env: map[string]string{
							"OVERRIDDEN": "step",
							"EXTRA":      "yes",
						},
					},
				},
			},
			want: ReplayPlan{
				Job: "test",
				Steps: []ReplayStep{
					{
						Name:    "Run",
						Command: "echo hi",
						Env:     []string{"CI=true", "EXTRA=yes", "NODE_ENV=production", "OVERRIDDEN=step"},
					},
				},
			},
		},
		{
			name: "secret-valued env is redacted",
			job: &Job{
				ID: "test",
				Env: map[string]string{
					"TOKEN": "${{ secrets.API_TOKEN }}",
					"PLAIN": "value",
				},
				Steps: []Step{
					{Name: "Run", Run: "echo hi"},
				},
			},
			want: ReplayPlan{
				Job: "test",
				Steps: []ReplayStep{
					{
						Name:    "Run",
						Command: "echo hi",
						Env:     []string{"PLAIN=value", "TOKEN=<secret>"},
					},
				},
			},
		},
		{
			name: "services and container become prerequisites",
			job: &Job{
				ID:        "db",
				Container: "node:24",
				Services: []Service{
					{ID: "postgres", Image: "postgres:15"},
					{ID: "redis", Image: "redis:7"},
				},
			},
			want: ReplayPlan{
				Job:   "db",
				Steps: []ReplayStep{},
				Prerequisites: []string{
					`postgres:15 must be reachable as "postgres"`,
					`redis:7 must be reachable as "redis"`,
					"steps run inside container node:24, not on the host",
				},
			},
		},
		{
			name: "job notes are copied through",
			job: &Job{
				ID: "test",
				Notes: []string{
					`job "test": if conditions are not modelled`,
					"strategy.matrix.include is not modelled",
				},
				Steps: []Step{
					{Name: "Run", Run: "echo hi"},
				},
			},
			want: ReplayPlan{
				Job: "test",
				Notes: []string{
					`job "test": if conditions are not modelled`,
					"strategy.matrix.include is not modelled",
				},
				Steps: []ReplayStep{
					{Name: "Run", Command: "echo hi"},
				},
			},
		},
		{
			name: "job with no steps",
			job:  &Job{ID: "test"},
			want: ReplayPlan{Job: "test", Steps: []ReplayStep{}},
		},
		{
			name: "nil job",
			job:  nil,
			want: ReplayPlan{},
		},
		{
			name: "unnamed run step gets a description from its command",
			job: &Job{
				ID: "test",
				Steps: []Step{
					{Run: "npm test\n# and then"},
				},
			},
			want: ReplayPlan{
				Job: "test",
				Steps: []ReplayStep{
					{Name: "npm test", Command: "npm test\n# and then"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Replay(tt.job); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Replay()\ngot:  %+v\nwant: %+v", got, tt.want)
			}
		})
	}
}

// TestReplayDeterministic asserts that two calls on the same job return
// identical plans, including the order of the merged env pairs. Env is sorted,
// prerequisites follow declaration order, and steps follow declaration order,
// so nothing can drift between calls.
func TestReplayDeterministic(t *testing.T) {
	job := &Job{
		ID: "build",
		Env: map[string]string{
			"B": "b",
			"A": "a",
		},
		Services: []Service{{ID: "postgres", Image: "postgres:15"}},
		Steps: []Step{
			{Name: "One", Run: "echo one", Env: map[string]string{"C": "c", "A": "step"}},
			{Name: "Two", Uses: "actions/setup-node@v4", With: map[string]string{"node-version": "20"}},
		},
	}

	first := Replay(job)
	second := Replay(job)
	if !reflect.DeepEqual(first, second) {
		t.Errorf("Replay is not deterministic:\nfirst:  %+v\nsecond: %+v", first, second)
	}

	// Replay must not let the caller mutate the input through the plan.
	first.Notes = append(first.Notes, "mutated")
	if len(job.Notes) != 0 {
		t.Errorf("mutating the plan changed the job notes: %v", job.Notes)
	}
}

// TestReplayMatrixIsStated guards the honesty of a matrix job's plan: its steps
// describe one combination out of several, and a reader who is not told that
// will reproduce a run that is not the one they care about.
func TestReplayMatrixIsStated(t *testing.T) {
	job := &Job{
		ID: "test",
		Matrix: map[string][]string{
			"os":         {"ubuntu-latest", "macos-latest"},
			"go-version": {"1.25", "1.26"},
		},
		Steps: []Step{{Name: "Test", Run: "go test ./..."}},
	}
	plan := Replay(job)
	want := "this job runs once per matrix combination (go-version=[1.25 1.26], os=[ubuntu-latest macos-latest]); the steps below describe one of them"
	if len(plan.Prerequisites) != 1 || plan.Prerequisites[0] != want {
		t.Errorf("prerequisites = %q, want [%q]", plan.Prerequisites, want)
	}
	// Keys are sorted, so the same job always renders the same line.
	if !reflect.DeepEqual(plan, Replay(job)) {
		t.Error("Replay is not deterministic for a matrix job")
	}
}

// TestReplayCheckoutNeedsNoAction covers the one action every workflow starts
// with: the reader is already standing in the checkout, so telling them to run
// it themselves would be the plan's first line and its worst advice.
func TestReplayCheckoutNeedsNoAction(t *testing.T) {
	plan := Replay(&Job{ID: "build", Steps: []Step{{Name: "Checkout", Uses: "actions/checkout@v4"}}})
	if len(plan.Steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(plan.Steps))
	}
	if !strings.Contains(plan.Steps[0].Reason, "nothing to do") {
		t.Errorf("reason = %q, want it to say there is nothing to do", plan.Steps[0].Reason)
	}
}

// TestReplaySetupVersionFromExpression covers a setup action whose version is
// itself an expression: naming "${{ matrix.go-version }}" as if it were a
// version would tell the reader to install something that does not exist.
func TestReplaySetupVersionFromExpression(t *testing.T) {
	plan := Replay(&Job{
		ID: "test",
		Steps: []Step{{
			Name: "Set up Go",
			Uses: "actions/setup-go@v5",
			With: map[string]string{"go-version": "${{ matrix.go-version }}"},
		}},
	})
	reason := plan.Steps[0].Reason
	if !strings.Contains(reason, "which the runner decides") || !strings.Contains(reason, "${{ matrix.go-version }}") {
		t.Errorf("reason = %q, want it to name the expression and say the runner decides it", reason)
	}
}

// TestReplayNoStepsSerializesAsEmptyArray guards the frozen machine contract:
// a job with no steps must serialize as "steps":[] so a consumer never has to
// treat null and [] as the same thing.
func TestReplayNoStepsSerializesAsEmptyArray(t *testing.T) {
	data, err := json.Marshal(Replay(&Job{ID: "test"}))
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !bytes.Contains(data, []byte(`"steps":[]`)) {
		t.Errorf("steps should serialize as [], got %s", data)
	}
	if bytes.Contains(data, []byte("null")) {
		t.Errorf("plan serialized with a null field: %s", data)
	}
}

// TestReplayRedactsSecretsInAnySpelling: a workflow may write ${{secrets.X}}
// with no spaces, and a redaction that only matches one spelling prints the
// other straight through.
func TestReplayRedactsSecretsInAnySpelling(t *testing.T) {
	for _, spelling := range []string{
		"${{ secrets.TOKEN }}",
		"${{secrets.TOKEN}}",
		"${{ SECRETS.TOKEN }}",
		"prefix ${{  secrets . TOKEN }}",
		"${{ secrets['TOKEN'] }}",
		`${{ secrets["TOKEN"] }}`,
		"${{secrets['TOKEN']}}",
		`${{ secrets[ "TOKEN" ] }}`,
	} {
		plan := Replay(&Job{
			ID:    "build",
			Env:   map[string]string{"TOKEN": spelling},
			Steps: []Step{{Name: "Test", Run: "go test ./..."}},
		})
		if got := plan.Steps[0].Env; len(got) != 1 || got[0] != "TOKEN=<secret>" {
			t.Errorf("env for %q = %q, want [TOKEN=<secret>]", spelling, got)
		}
	}
}
