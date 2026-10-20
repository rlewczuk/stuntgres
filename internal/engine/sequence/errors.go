package sequence

import (
	"strings"

	"stuntgres/internal/sqlstate"
)

// sqlError is the engine's plain ERROR constructor; a private copy, as in
// visibility, lock, access and routine.
func sqlError(code, message string) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: code, Message: message,
	}
}

// commentLiteral quotes a comment as an escape string literal, exactly as
// the engine's dump does.
func commentLiteral(text string) string {
	return "E'" + strings.ReplaceAll(strings.ReplaceAll(text, "\\", "\\\\"), "'", "''") + "'"
}
