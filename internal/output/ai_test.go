package output

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/nyrvo-dev/nyrvo/internal/analysis"
	"github.com/nyrvo-dev/nyrvo/internal/diff"
	"github.com/nyrvo-dev/nyrvo/internal/finding"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

func TestAIRequestTextDisclosesUnexecutedRequest(t *testing.T) {
	prompt := "Compare these environments.\nExplain the likely cause without guessing."
	got := render(t, func(w io.Writer) error {
		return AIRequestText(w, analysis.Input{}, prompt)
	})

	for _, want := range []string{
		"AI ANALYSIS REQUEST (NOT EXECUTED)",
		"No agent was selected or run.",
		"Two environment snapshots, their differences, and Nyrvo's own findings.",
		"Nothing was executed, and nothing left this machine.",
		"No command was invoked.",
		"Environment variable values are never recorded",
		"only the names this diagnosis refers to are included",
		"Home directory prefixes in paths are replaced with ~.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("AIRequestText output missing %q:\n%s", want, got)
		}
	}

	for _, point := range []string{"Agent", "Data", "Execution", "Command"} {
		if !strings.Contains(got, "  "+point) {
			t.Errorf("AIRequestText output missing %s disclosure: %s", point, got)
		}
	}
}

func TestAIRequestTextPreservesPromptBetweenMarkers(t *testing.T) {
	for _, prompt := range []string{
		"First line\n\nThird\tline without a trailing newline",
		"First line\nSecond line\n",
	} {
		got := render(t, func(w io.Writer) error {
			return AIRequestText(w, analysis.Input{}, prompt)
		})

		marked := aiRequestBegin + "\n" + prompt + "\n" + aiRequestEnd
		if !strings.Contains(got, marked) {
			t.Errorf("prompt %q was not preserved between request markers:\n%s", prompt, got)
		}
		if !strings.Contains(got, "Paste this request into the agent of your choice.") {
			t.Errorf("AIRequestText output missing closing instruction: %s", got)
		}
	}
}

func TestAIRequestJSONRoundTrip(t *testing.T) {
	in := analysis.Input{
		SchemaVersion: 7,
		A:             &snapshot.Snapshot{Name: "local", SchemaVersion: 7},
		B:             &snapshot.Snapshot{Name: "ci", SchemaVersion: 7},
		Differences: []diff.Difference{{
			Component: diff.ComponentRuntime,
			Key:       "go",
			Kind:      diff.KindChanged,
			A:         "1.25.0",
			B:         "1.26.0",
		}},
		Findings: []finding.Finding{{
			Rule:        finding.RuntimeVersionMismatch,
			Severity:    finding.SeverityHigh,
			Component:   diff.ComponentRuntime,
			Key:         "go",
			Expected:    "1.26.0",
			Actual:      "1.25.0",
			Description: "Go versions differ.",
		}},
	}
	prompt := "Rank the evidence.\nDo not invent facts."
	var buf bytes.Buffer
	if err := AIRequestJSON(&buf, in, prompt); err != nil {
		t.Fatalf("AIRequestJSON: %v", err)
	}

	var got aiRequestDoc
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal AI request: %v", err)
	}
	if got.Executed {
		t.Error("AIRequestJSON reported that a model executed")
	}
	if got.Prompt != prompt {
		t.Errorf("prompt = %q, want %q", got.Prompt, prompt)
	}
	if got.SchemaVersion != in.SchemaVersion {
		t.Errorf("schema_version = %d, want %d", got.SchemaVersion, in.SchemaVersion)
	}
	if !reflect.DeepEqual(got.Input.Differences, in.Differences) {
		t.Errorf("differences did not survive round-trip: got %+v, want %+v", got.Input.Differences, in.Differences)
	}
	if !reflect.DeepEqual(got.Input.Findings, in.Findings) {
		t.Errorf("findings did not survive round-trip: got %+v, want %+v", got.Input.Findings, in.Findings)
	}
}

func TestAIRequestJSONIsSingleDocument(t *testing.T) {
	var buf bytes.Buffer
	if err := AIRequestJSON(&buf, analysis.Input{SchemaVersion: 3}, "Inspect this."); err != nil {
		t.Fatalf("AIRequestJSON: %v", err)
	}

	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	var doc aiRequestDoc
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("decode JSON document: %v", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("content follows JSON document: %v", err)
	}
	if first := strings.TrimSpace(buf.String()); !strings.HasPrefix(first, "{") || !strings.HasSuffix(first, "}") {
		t.Errorf("JSON output has text outside document: %q", buf.String())
	}
}
