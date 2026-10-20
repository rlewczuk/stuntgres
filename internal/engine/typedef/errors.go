package typedef

import (
	"strconv"

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

// insertError is sqlError with a statement position, like the engine's helper.
func insertError(code, message string, pos parser.Position) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: strconv.Itoa(pos.Character)}
}
