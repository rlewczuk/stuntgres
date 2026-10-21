package role

import "stuntgres/internal/sqlstate"

// sqlError builds an ERROR with the given SQLSTATE, like the engine's helper.
func sqlError(code, message string) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: code, Message: message,
	}
}
