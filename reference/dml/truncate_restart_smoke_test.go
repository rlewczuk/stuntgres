package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTruncateRestartFeatureSmoke(t *testing.T) {
	truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
		for _, q := range []string{"CREATE TABLE s(id SERIAL PRIMARY KEY)", "CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(START 44))", "CREATE SEQUENCE u START 70"} {
			truncateExec(t, f, f.A, q, 0)
		}
		truncateExec(t, f, f.A, "INSERT INTO s DEFAULT VALUES", 1)
		truncateExec(t, f, f.A, "INSERT INTO s DEFAULT VALUES", 1)
		truncateExec(t, f, f.A, "INSERT INTO i DEFAULT VALUES", 1)
		truncateRestartValue(t, f, f.A, "SELECT nextval('u')", "nextval", 70)
		q := "TRUNCATE s,i RESTART IDENTITY"
		x := truncateRestartRoute(t, p, f.A, q, held)
		truncateRows(t, f, f.A, "s", 1, 2)
		truncateRows(t, f, f.A, "i", 44)
		defer f.A.ExecContext(p.Ctx, "ROLLBACK")
		truncateExec(t, f, f.A, "BEGIN", 0)
		truncateRestartValue(t, f, f.A, "SELECT nextval('s_id_seq')", "nextval", 3)
		truncateExec(t, f, x, q, 0)
		truncateExec(t, f, f.A, "INSERT INTO i DEFAULT VALUES", 1)
		truncateExec(t, f, f.A, "INSERT INTO s DEFAULT VALUES", 1)
		truncateExec(t, f, f.A, "ROLLBACK", 0)
		truncateRows(t, f, f.A, "s", 1, 2)
		truncateRows(t, f, f.A, "i", 44)
		truncateRestartValue(t, f, f.A, "SELECT currval('s_id_seq')", "currval", 1)
		truncateRestartValue(t, f, f.A, "SELECT lastval()", "lastval", 1)
		truncateRestartValue(t, f, f.B, "SELECT nextval('s_id_seq')", "nextval", 4)
		truncateRestartValue(t, f, f.B, "SELECT nextval('u')", "nextval", 71)
		truncateExec(t, f, x, q, 0)
		truncateExec(t, f, f.A, "INSERT INTO s DEFAULT VALUES", 1)
		truncateExec(t, f, f.A, "INSERT INTO i DEFAULT VALUES", 1)
		truncateRows(t, f, f.A, "s", 1)
		truncateRows(t, f, f.A, "i", 44)
		truncateExec(t, f, f.A, "TRUNCATE s,i CONTINUE IDENTITY", 0)
		truncateExec(t, f, f.A, "INSERT INTO s DEFAULT VALUES", 1)
		truncateExec(t, f, f.A, "INSERT INTO i DEFAULT VALUES", 1)
		truncateRows(t, f, f.A, "s", 2)
		truncateRows(t, f, f.A, "i", 45)
		truncateExec(t, f, f.A, "CREATE ROLE restart_smoke_reader", 0)
		defer func() {
			f.A.ExecContext(p.Ctx, "RESET ROLE")
			f.A.ExecContext(p.Ctx, "DROP OWNED BY restart_smoke_reader")
			f.A.ExecContext(p.Ctx, "DROP ROLE restart_smoke_reader")
		}()
		truncateExec(t, f, f.A, "GRANT USAGE ON SCHEMA public TO restart_smoke_reader", 0)
		truncateExec(t, f, f.A, "GRANT TRUNCATE ON s TO restart_smoke_reader", 0)
		singleQ := "TRUNCATE s RESTART IDENTITY"
		single := truncateRestartRoute(t, p, f.A, singleQ, held)
		truncateExec(t, f, f.A, "SET ROLE restart_smoke_reader", 0)
		truncateRestartError(t, f, single, singleQ, truncateWant("42501", "must be owner of sequence s_id_seq"))
		truncateExec(t, f, f.A, "RESET ROLE", 0)
		truncateRows(t, f, f.A, "s", 2)
		truncateRestartState(t, f, f.A, "s_id_seq", 2, true)
		truncateExec(t, f, single, singleQ, 0)
		truncateExec(t, f, f.A, "INSERT INTO s DEFAULT VALUES", 1)
		tx := truncateBegin(t, p, f.B, nil)
		truncateRestartState(t, f, tx, "s_id_seq", 1, true)
		truncateExec(t, f, f.A, "SET lock_timeout='30ms'", 0)
		truncateRestartError(t, f, x, q, truncateWant("55P03", "canceling statement due to lock timeout"))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		truncateExec(t, f, f.A, "SET lock_timeout=0", 0)
		truncateRows(t, f, f.A, "s", 1)
		truncateRows(t, f, f.A, "i", 45)
		truncateRestartState(t, f, f.A, "s_id_seq", 1, true)
		truncateRestartState(t, f, f.A, "i_id_seq", 45, true)
		truncateExec(t, f, x, q, 0)
		truncateExec(t, f, f.A, "INSERT INTO s DEFAULT VALUES", 1)
		truncateExec(t, f, f.A, "INSERT INTO i DEFAULT VALUES", 1)
		truncateRows(t, f, f.A, "s", 1)
		truncateRows(t, f, f.A, "i", 44)
	})
}
