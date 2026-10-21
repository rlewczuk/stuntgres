package engine

import "errors"

// registrationError uses the engine RegistrationError message prefix.
func registrationError(message string) error { return errors.New("stuntgres: " + message) }
