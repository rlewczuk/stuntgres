package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// LSEG shares the BOX scenario lifecycle and geometry adapter assertions.
func lsegScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	boxScenario(t, body)
}
