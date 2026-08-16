package output

import (
	"fmt"
	"io"
	"strings"
)

// ConfigSetting is one preference and its current value.
type ConfigSetting struct {
	Key string
	// Value is empty when the key is not set; the renderer says so rather than
	// printing a blank column, which reads as a value of nothing.
	Value string
}

// ConfigList renders the preferences and where they are stored.
//
// Every known key is listed, set or not: showing only what is set leaves a user
// guessing what else exists. The path is printed because a setting that decides
// which program Nyrvo runs should never be somewhere they have to go looking
// for.
func ConfigList(w io.Writer, path string, settings []ConfigSetting) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", path)
	for _, s := range settings {
		value := s.Value
		if value == "" {
			value = "(not set)"
		}
		fmt.Fprintf(&b, "  %s\t%s\n", s.Key, value)
	}
	return writeAligned(w, b.String())
}
