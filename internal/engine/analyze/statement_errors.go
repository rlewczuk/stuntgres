package analyze

import (
	"strconv"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func sqlError(code, message string) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: code, Message: message,
	}
}

// insertError reports a statement error positioned at pos, as the root
// engine's helper of the same name does.
func insertError(code, message string, pos parser.Position) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: strconv.Itoa(pos.Character)}
}
