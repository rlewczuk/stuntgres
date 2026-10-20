package catalog

import (
	"strings"

	"stuntgres/internal/parser"
)

// ObjectNameText joins a parsed object name's parts with dots, unquoted.
func ObjectNameText(n parser.ObjectName) string {
	parts := make([]string, len(n.Parts))
	for i, p := range n.Parts {
		parts[i] = p.Text
	}
	return strings.Join(parts, ".")
}
