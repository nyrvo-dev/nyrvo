package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/nyrvo-dev/nyrvo/internal/agent"
	"github.com/nyrvo-dev/nyrvo/internal/config"
	"github.com/nyrvo-dev/nyrvo/internal/output"
)

func runConfig(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageErr("config takes a subcommand: set, unset or list")
	}
	switch args[0] {
	case "set":
		return runConfigSet(args[1:], stdout)
	case "unset":
		return runConfigUnset(args[1:], stdout)
	case "list":
		return runConfigList(args[1:], stdout)
	default:
		return usageErr("unknown config subcommand %q; use set, unset or list", args[0])
	}
}

func runConfigSet(args []string, stdout io.Writer) error {
	if len(args) != 2 {
		return usageErr("config set takes a key and a value: nyrvo config set ai.agent opencode")
	}
	key, value := args[0], args[1]
	// A value is checked before it is stored, not when it is used. A stored
	// agent name that resolves to nothing would otherwise fail much later, in a
	// command the user did not connect to the one that accepted it.
	if key == "ai.agent" {
		if _, ok := agent.Lookup(value); !ok {
			return usageErr("%q is not an agent Nyrvo knows; use one of %s", value, strings.Join(agent.Names(), ", "))
		}
	}

	c, err := config.Load()
	if err != nil {
		return err
	}
	if err := c.Set(key, value); err != nil {
		return usageErr("%v", err)
	}
	if err := config.Save(c); err != nil {
		return err
	}
	path, err := config.Path()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s = %s\nSaved to %s\n", key, value, path)
	return err
}

func runConfigUnset(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return usageErr("config unset takes exactly one key")
	}
	c, err := config.Load()
	if err != nil {
		return err
	}
	if err := c.Unset(args[0]); err != nil {
		return usageErr("%v", err)
	}
	if err := config.Save(c); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s unset\n", args[0])
	return err
}

func runConfigList(args []string, stdout io.Writer) error {
	if len(args) > 0 {
		return usageErr("config list takes no arguments")
	}
	c, err := config.Load()
	if err != nil {
		return err
	}
	path, err := config.Path()
	if err != nil {
		return err
	}
	settings := make([]output.ConfigSetting, 0, len(config.Keys()))
	for _, key := range config.Keys() {
		value, _ := c.Get(key)
		settings = append(settings, output.ConfigSetting{Key: key, Value: value})
	}
	return output.ConfigList(stdout, path, settings)
}

// configuredAgent resolves the default agent from configuration.
//
// An unreadable config is reported rather than ignored: it states which program
// the user asked Nyrvo to run, and carrying on without it would run a different
// one, or none, without saying so.
func configuredAgent() (*agent.Agent, error) {
	c, err := config.Load()
	if err != nil {
		return nil, err
	}
	return agentFromConfig(c)
}

// agentFromConfig is separate from reading the file so the resolution — the
// part with rules in it — can be tested without a config directory.
func agentFromConfig(c *config.Config) (*agent.Agent, error) {
	name, _ := c.Get("ai.agent")
	if name == "" {
		return nil, nil
	}
	selected, ok := agent.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("configured ai.agent %q is not an agent Nyrvo knows; run: nyrvo config set ai.agent <%s>",
			name, strings.Join(agent.Names(), "|"))
	}
	if !selected.Available() {
		return nil, fmt.Errorf("configured ai.agent %q is not installed; install it, choose another (%s), or pass --agent=none to print the request instead",
			name, strings.Join(agent.Names(), ", "))
	}
	return &selected, nil
}
