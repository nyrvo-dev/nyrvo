package cli

import (
	"strings"
	"testing"

	"github.com/nyrvo-dev/nyrvo/internal/config"
)

func TestAgentFromConfigResolvesAKnownName(t *testing.T) {
	var c config.Config
	if err := c.Set("ai.agent", "claude"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := agentFromConfig(&c)
	if err != nil {
		// The agent has to be installed for this to resolve; on a machine
		// without it the honest outcome is the "not installed" error, not a
		// silent nil.
		if !strings.Contains(err.Error(), "not installed") {
			t.Fatalf("agentFromConfig: %v", err)
		}
		return
	}
	if got == nil || got.Name() != "claude" {
		t.Fatalf("agent = %+v, want claude", got)
	}
}

func TestAgentFromConfigIsNilWhenUnset(t *testing.T) {
	// No configuration means --ai keeps its original behaviour: build the
	// request and print it.
	got, err := agentFromConfig(&config.Config{})
	if err != nil {
		t.Fatalf("agentFromConfig: %v", err)
	}
	if got != nil {
		t.Fatalf("agent = %+v, want none", got)
	}
}

func TestAgentFromConfigRejectsAnUnknownName(t *testing.T) {
	// A name that resolves to nothing must be reported rather than ignored: the
	// user stated which program to run, and quietly running none instead is the
	// one outcome they did not ask for.
	c := &config.Config{}
	c.AI.Agent = "bogus"
	_, err := agentFromConfig(c)
	if err == nil {
		t.Fatal("an unknown configured agent was accepted")
	}
	if !strings.Contains(err.Error(), "config set ai.agent") {
		t.Errorf("error = %q, want it to say how to fix the configuration", err)
	}
}

func TestAgentNoneOverridesAConfiguredDefault(t *testing.T) {
	// The escape hatch: someone who wants the request as text, this once, must
	// not have to unset their configuration to get it.
	got, err := selectAgent(noAgent)
	if err != nil {
		t.Fatalf("selectAgent(%q): %v", noAgent, err)
	}
	if got != nil {
		t.Fatalf("agent = %+v, want none", got)
	}
}
