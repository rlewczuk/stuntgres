package ddl

import "stuntgres/internal/sqlstate"

// sqlError builds an ERROR with the given SQLSTATE, like the engine's helper.
func sqlError(code, message string) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: code, Message: message,
	}
}

// detailError is sqlError with a detail line.
func detailError(code, message, detail string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
}
