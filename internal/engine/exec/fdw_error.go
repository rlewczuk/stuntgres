package exec

import (
	"database/sql/driver"
	"errors"
)

// database/sql reserves these two sentinels for replay/fallback of operations
// that did not execute. A provider callback has already been entered, so those
// classifications cannot cross the accepted callback boundary (especially an
// uncertain Commit). Preserve the text, not an unwrap/Is path to either sentinel.
// Every other provider error retains its exact identity and diagnostics.
func ForeignCallbackError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, driver.ErrSkip) {
		return errors.New("stuntgres: foreign provider contract violation: reserved database/sql driver error: " + err.Error())
	}
	return err
}
