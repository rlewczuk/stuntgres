package fixture

import (
	"database/sql"
	"database/sql/driver"
	"testing"
)

// SettingsPhysical compares the public physical driver object, never an internal token or unsafe pointer.
func SettingsPhysical(t *testing.T, c *sql.Conn) any {
	t.Helper()
	var identity any
	if err := c.Raw(func(raw any) error {
		identity = raw
		if validator, ok := raw.(driver.Validator); ok && !validator.IsValid() {
			t.Fatal("physical adapter is invalid")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return identity
}
