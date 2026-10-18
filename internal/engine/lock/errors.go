package lock

import (
	"errors"

	"stuntgres/internal/sqlstate"
)

var (
	// ErrOwnerEnded is the outcome of a request whose ticket ended first.
	ErrOwnerEnded = errors.New("stuntgres: transaction ended")
	// ErrUnavailable is the outcome of a NOWAIT request that would wait.
	ErrUnavailable = errors.New("stuntgres: relation lock unavailable")
)

func sqlError(code, message string) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: code, Message: message,
	}
}
