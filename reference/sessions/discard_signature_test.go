package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDiscardPlansSignatureFence(t *testing.T) {
	discardScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		discardExec(t, p, b, a, "CREATE TABLE discard_signature(v int)")
		sqlPreparedStep(t, p, b, a, fixture.Exec("INSERT INTO discard_signature VALUES(7)", 1))
		discardExec(t, p, b, a, "PREPARE p AS SELECT * FROM discard_signature")
		query := "SELECT * FROM discard_signature"
		stmt, err := a.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: a, Statement: stmt, SQL: query}
		discardScalar(t, p, b, a, "EXECUTE p", "v", "INT4", int64(7))
		discardScalar(t, p, b, held, query, "v", "INT4", int64(7))
		discardExec(t, p, b, a, "ALTER TABLE discard_signature ADD COLUMN other text")
		discardExec(t, p, b, a, "DISCARD PLANS")
		for range 2 {
			discardError(t, p, b, a, "EXECUTE p", "0A000", "cached plan must not change result type", "")
			discardError(t, p, b, held, query, "0A000", "cached plan must not change result type", "")
		}
		// Definitions and handles remain installed after rejected reanalysis.
		// Restore the original result shape through supported DDL, not re-PREPARE.
		discardExec(t, p, b, a, "ALTER TABLE discard_signature DROP COLUMN other")
		discardExec(t, p, b, a, "DISCARD PLANS")
		discardScalar(t, p, b, a, "EXECUTE p", "v", "INT4", int64(7))
		discardScalar(t, p, b, held, query, "v", "INT4", int64(7))
	})
}
