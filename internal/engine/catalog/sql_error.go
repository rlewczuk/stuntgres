package catalog

import "stuntgres/internal/sqlstate"

// sqlError is the catalog copy of the engine's plain ERROR diagnostic.
func sqlError(code, message string) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: code, Message: message,
	}
}
