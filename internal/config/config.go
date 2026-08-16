// Package config stores user-level Nyrvo preferences.
//
// Configuration is deliberately never read from a project. Because it selects
// a program Nyrvo will execute, repository-level configuration would let the
// author of a pull request choose which program runs on another user's machine.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxConfigSize = 1 << 20 // 1 MiB

var userConfigDir = os.UserConfigDir

// Config contains the preferences Nyrvo understands.
type Config struct {
	AI struct {
		Agent string `json:"agent,omitempty"`
	} `json:"ai,omitempty"`
}

type keyBinding struct {
	get func(*Config) string
	set func(*Config, string)
}

var keyBindings = map[string]keyBinding{
	"ai.agent": {
		get: func(c *Config) string { return c.AI.Agent },
		set: func(c *Config, value string) { c.AI.Agent = value },
	},
}

// Path reports where the config file lives.
func Path() (string, error) {
	dir, err := userConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(dir, "nyrvo", "config.json"), nil
}

// Load reads the user configuration. A missing file represents default
// configuration rather than an installation error.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	// Reading one byte past the limit proves the file is oversized without
	// allocating according to input that has no legitimate reason to be large.
	data, err := io.ReadAll(io.LimitReader(f, maxConfigSize+1))
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	if len(data) > maxConfigSize {
		return nil, fmt.Errorf("read config %s: file exceeds %d bytes", path, maxConfigSize)
	}

	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		// Unlike malformed repository requirements, this is the user's own stated
		// intent; ignoring it could silently execute a different agent.
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &c, nil
}

// Save atomically replaces the user configuration.
func Save(c *Config) error {
	if c == nil {
		return errors.New("save config: nothing to save")
	}
	path, err := Path()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory %s: %w", dir, err)
	}
	// The directory may predate this store; owner-only access prevents another
	// local user from replacing configuration that selects an executable.
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("set config directory permissions %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".config.*.tmp")
	if err != nil {
		return fmt.Errorf("create config file %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write config %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	// Owner-only access matters even without secrets because changing this file
	// changes which program Nyrvo executes.
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("set config permissions %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("save config %s: %w", path, err)
	}
	return nil
}

// Keys lists the settable configuration keys in lexical order.
func Keys() []string {
	keys := make([]string, 0, len(keyBindings))
	for key := range keyBindings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Get returns a key's current value and whether Nyrvo knows the key.
func (c *Config) Get(key string) (value string, known bool) {
	binding, ok := keyBindings[key]
	if !ok {
		return "", false
	}
	return binding.get(c), true
}

// Set assigns a non-empty value to a known key.
func (c *Config) Set(key, value string) error {
	binding, ok := keyBindings[key]
	if !ok {
		return unknownKeyError(key)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("set config key %q: value must not be empty; use unset instead", key)
	}
	// Stored trimmed, not merely validated trimmed: " opencode " would be
	// checked as non-empty and then saved with the spaces, and the lookup that
	// resolves it to an agent would fail on a value the user cannot see is wrong.
	binding.set(c, value)
	return nil
}

// Unset clears a known key.
func (c *Config) Unset(key string) error {
	binding, ok := keyBindings[key]
	if !ok {
		return unknownKeyError(key)
	}
	binding.set(c, "")
	return nil
}

func unknownKeyError(key string) error {
	return fmt.Errorf("unknown config key %q; known keys: %s", key, strings.Join(Keys(), ", "))
}
