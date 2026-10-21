package table

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

// insertError is sqlError with the 1-based character position of pos, like the
// engine's helper; it always sets Position, even "0".
func insertError(code, message string, pos parser.Position) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: strconv.Itoa(pos.Character)}
}
