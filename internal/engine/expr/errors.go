package expr

import (
	"errors"
	"strconv"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// ErrorAt returns an ERROR with the SQLSTATE code and message, positioned at
// pos. A zero position leaves the error unpositioned.
func ErrorAt(code, message string, pos parser.Position) error {
	d := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if pos.Character > 0 {
		d.Position = strconv.Itoa(pos.Character)
	}
	return d
}

// WithPosition returns a copy of the SQLSTATE error in err positioned at pos,
// including a zero position. Other errors are returned unchanged.
func WithPosition(err error, pos parser.Position) error {
	var diagnostic *sqlstate.Error
	if errors.As(err, &diagnostic) {
		copy := *diagnostic
		copy.Position = strconv.Itoa(pos.Character)
		return &copy
	}
	return err
}
