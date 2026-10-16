package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTruncateRestartTransactions(t *testing.T) {
	truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
		truncateRestartSeed(t, f)
		q := "TRUNCATE t RESTART IDENTITY"
		// SQL BEGIN keeps the same physical Conn and the same preheld handle.
		x := truncateRestartRoute(t, p, f.A, q, held)
		defer f.A.ExecContext(p.Ctx, "ROLLBACK")
		truncateExec(t, f, f.A, "BEGIN", 0)
		truncateRestartValue(t, f, f.A, "SELECT nextval('t_id_seq')", "nextval", 3)
		truncateExec(t, f, x, q, 0)
		truncateRestartValue(t, f, f.A, "SELECT currval('t_id_seq')", "currval", 3)
		truncateRestartValue(t, f, f.A, "SELECT lastval()", "lastval", 3)
		truncateExec(t, f, f.A, "INSERT INTO t DEFAULT VALUES", 1)
		truncateRows(t, f, f.A, "t", 1)
		truncateExec(t, f, f.A, "ROLLBACK", 0)
		truncateRows(t, f, f.A, "t", 1, 2)
		truncateRestartValue(t, f, f.A, "SELECT currval('t_id_seq')", "currval", 1)
		truncateRestartValue(t, f, f.A, "SELECT lastval()", "lastval", 1)
		truncateRestartValue(t, f, f.B, "SELECT nextval('t_id_seq')", "nextval", 4)
		truncateExec(t, f, f.A, "BEGIN", 0)
		truncateExec(t, f, x, q, 0)
		truncateExec(t, f, f.A, "INSERT INTO t DEFAULT VALUES", 1)
		truncateExec(t, f, f.A, "COMMIT", 0)
		truncateRows(t, f, f.B, "t", 1)
		truncateRestartValue(t, f, f.B, "SELECT nextval('t_id_seq')", "nextval", 2)
		// Ordinary allocations and setval are not transactional undo state.
		truncateExec(t, f, f.A, "BEGIN", 0)
		truncateRestartValue(t, f, f.A, "SELECT nextval('t_id_seq')", "nextval", 3)
		truncateExec(t, f, f.A, "ROLLBACK", 0)
		truncateRestartValue(t, f, f.B, "SELECT nextval('t_id_seq')", "nextval", 4)
		truncateExec(t, f, f.A, "BEGIN", 0)
		truncateRestartValue(t, f, f.A, "SELECT setval('t_id_seq',80)", "setval", 80)
		truncateExec(t, f, f.A, "ROLLBACK", 0)
		truncateRestartValue(t, f, f.B, "SELECT nextval('t_id_seq')", "nextval", 81)
	})
}

func TestReferenceTruncateRestartFailure(t *testing.T) {
	t.Run("ConstraintAbort", func(t *testing.T) {
		truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
			truncateRestartSeed(t, f)
			q := "TRUNCATE t RESTART IDENTITY"
			x := truncateRestartRoute(t, p, f.A, q, held)
			defer f.A.ExecContext(p.Ctx, "ROLLBACK")
			truncateExec(t, f, f.A, "BEGIN", 0)
			truncateRestartValue(t, f, f.A, "SELECT nextval('t_id_seq')", "nextval", 3)
			truncateExec(t, f, x, q, 0)
			truncateExec(t, f, f.A, "INSERT INTO t DEFAULT VALUES", 1)
			d := truncateWant("23502", `null value in column "id" of relation "t" violates not-null constraint`)
			d.Detail = "Failing row contains (null)."
			d.Schema, d.Table, d.Column = "public", "t", "id"
			truncateRestartError(t, f, f.A, "INSERT INTO t VALUES(NULL)", d)
			truncateRestartError(t, f, x, q, truncateWant("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
			truncateExec(t, f, f.A, "ROLLBACK", 0)
			truncateRows(t, f, f.A, "t", 1, 2)
			truncateRestartValue(t, f, f.A, "SELECT currval('t_id_seq')", "currval", 1)
			truncateRestartValue(t, f, f.A, "SELECT lastval()", "lastval", 1)
			truncateRestartValue(t, f, f.B, "SELECT nextval('t_id_seq')", "nextval", 4)
			truncateExec(t, f, x, q, 0)
			truncateExec(t, f, f.A, "INSERT INTO t DEFAULT VALUES", 1)
			truncateRows(t, f, f.A, "t", 1)
		})
	})
	t.Run("SavepointsAndRepeatedReset", func(t *testing.T) {
		truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
			truncateRestartSeed(t, f)
			truncateExec(t, f, f.A, "CREATE TABLE other(id INT)", 0)
			q := "TRUNCATE t RESTART IDENTITY"
			x := truncateRestartRoute(t, p, f.A, q, held)
			defer f.A.ExecContext(p.Ctx, "ROLLBACK")
			truncateExec(t, f, f.A, "BEGIN", 0)
			truncateExec(t, f, f.A, "INSERT INTO other VALUES(7)", 1)
			truncateExec(t, f, f.A, "DELETE FROM t WHERE id=2", 1)
			truncateRestartValue(t, f, f.A, "SELECT nextval('t_id_seq')", "nextval", 3)
			truncateExec(t, f, f.A, "SAVEPOINT before_reset", 0)
			truncateExec(t, f, x, q, 0)
			truncateExec(t, f, f.A, "INSERT INTO t DEFAULT VALUES", 1)
			truncateExec(t, f, f.A, "ROLLBACK TO before_reset", 0)
			truncateRows(t, f, f.A, "t", 1)
			truncateRows(t, f, f.A, "other", 7)
			truncateRestartValue(t, f, f.A, "SELECT currval('t_id_seq')", "currval", 1)
			truncateRestartValue(t, f, f.A, "SELECT lastval()", "lastval", 1)
			// The sequence AX acquired after the savepoint was released.
			truncateRestartValue(t, f, f.B, "SELECT nextval('t_id_seq')", "nextval", 4)
			truncateExec(t, f, x, q, 0)
			truncateRestartValue(t, f, f.A, "SELECT nextval('t_id_seq')", "nextval", 1)
			truncateExec(t, f, f.A, "SAVEPOINT after_reset", 0)
			truncateRestartValue(t, f, f.A, "SELECT nextval('t_id_seq')", "nextval", 2)
			truncateExec(t, f, f.A, "ROLLBACK TO after_reset", 0)
			// A savepoint does not snapshot counters of an already-private generator.
			truncateRestartValue(t, f, f.A, "SELECT nextval('t_id_seq')", "nextval", 3)
			truncateExec(t, f, x, q, 0)
			truncateExec(t, f, f.A, "INSERT INTO t DEFAULT VALUES", 1)
			truncateExec(t, f, f.A, "COMMIT", 0)
			truncateRows(t, f, f.B, "t", 1)
			truncateRows(t, f, f.B, "other", 7)
			truncateRestartValue(t, f, f.B, "SELECT nextval('t_id_seq')", "nextval", 2)
		})
	})
	t.Run("OwnSequenceDDL", func(t *testing.T) {
		truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
			truncateExec(t, f, f.A, "CREATE TABLE t(id INT)", 0)
			truncateExec(t, f, f.A, "CREATE SEQUENCE s START 10", 0)
			q := "TRUNCATE t RESTART IDENTITY"
			x := truncateRestartRoute(t, p, f.A, q, held)
			defer f.A.ExecContext(p.Ctx, "ROLLBACK")
			truncateExec(t, f, f.A, "BEGIN", 0)
			truncateExec(t, f, f.A, "ALTER SEQUENCE s START 33 OWNED BY t.id", 0)
			truncateRestartValue(t, f, f.A, "SELECT nextval('s')", "nextval", 10)
			truncateExec(t, f, x, q, 0)
			truncateRestartValue(t, f, f.A, "SELECT nextval('s')", "nextval", 33)
			truncateExec(t, f, f.A, "CREATE SEQUENCE fresh START 70 OWNED BY t.id", 0)
			truncateRestartValue(t, f, f.A, "SELECT nextval('fresh')", "nextval", 70)
			truncateExec(t, f, x, q, 0)
			truncateRestartValue(t, f, f.A, "SELECT nextval('fresh')", "nextval", 70)
			truncateExec(t, f, f.A, "ROLLBACK", 0)
			// Pinned PostgreSQL restores 10 here: the draw followed own
			// transactional ALTER, unlike the unchanged-generator ordinary
			// allocation rollback controls in Transactions.
			truncateRestartValue(t, f, f.B, "SELECT nextval('s')", "nextval", 10)
			d := truncateWant("42P01", `relation "fresh" does not exist`)
			d.Position = "16"
			truncateRestartError(t, f, f.A, "SELECT nextval('fresh')", d)
		})
	})
}
