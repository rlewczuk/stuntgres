package engine

import (
	"strings"
	"stuntgres/internal/parser"
)

// Inputs have already passed stored.ViewOptions/RESET validation. PostgreSQL retains
// unmatched old options in their old order, then appends SET keys in statement
// order. Parser values preserve lexical strings (including explicit false).
// Every mutation owns its output slice; old definitions remain immutable.
func transformViewOptions(old []string, changes []parser.ViewOption, reset bool) []string {
	if len(changes) == 0 {
		return old
	}
	capacity := len(old)
	if !reset {
		capacity += len(changes)
	}
	out := make([]string, 0, capacity)
	for _, installed := range old {
		name, _, _ := strings.Cut(installed, "=")
		touched := false
		for _, change := range changes {
			if change.Name.Text == name {
				touched = true
				break
			}
		}
		if !touched {
			out = append(out, installed)
		}
	}
	if !reset {
		for _, change := range changes {
			out = append(out, change.Name.Text+"="+change.Value)
		}
	}
	return out
}
