package fdw

import (
	"errors"
	"stuntgres/internal/sqlstate"
)

// registrationError uses the engine RegistrationError message prefix.
func registrationError(message string) error { return errors.New("stuntgres: " + message) }

// sqlError builds an ERROR with the given SQLSTATE, like the engine's helper.
func sqlError(code, message string) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: code, Message: message,
	}
}
