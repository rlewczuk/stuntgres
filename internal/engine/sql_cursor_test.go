package engine

import (
	"testing"

	"stuntgres/internal/engine/session"
)

func cursorUnitSetup(t *testing.T) *session.Session {
	t.Helper()
	s := sessionStart(t, New(), nil)
	sessionSQL(t, s, "CREATE TABLE unit_rows(i INT NOT NULL)")
	sessionSQL(t, s, "INSERT INTO unit_rows VALUES(1),(2),(3)")
	sessionSQL(t, s, "BEGIN")
	return s
}
