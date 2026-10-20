package routine

import (
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// sqlError builds an ERROR with the given SQLSTATE, like the engine's helper.
func sqlError(code, message string) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: code, Message: message,
	}
}

// objectNameText joins the parts of a parsed name with dots, like the
// engine's helper; quoting is not reapplied.
func objectNameText(n parser.ObjectName) string {
	parts := make([]string, len(n.Parts))
	for i, p := range n.Parts {
		parts[i] = p.Text
	}
	return strings.Join(parts, ".")
}
