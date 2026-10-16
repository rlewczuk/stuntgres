package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTruncateRestartCache(t *testing.T) {
	t.Run("InitiatingTailWithoutDemand", func(t *testing.T) {
		truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
			truncateExec(t, f, f.A, "CREATE TABLE t(id SERIAL)", 0)
			truncateExec(t, f, f.A, "ALTER SEQUENCE t_id_seq CACHE 4", 0)
			truncateRestartValue(t, f, f.A, "SELECT nextval('t_id_seq')", "nextval", 1)
			truncateRestartState(t, f, f.A, "t_id_seq", 4, true)
			q := "TRUNCATE t RESTART IDENTITY"
			x := truncateRestartRoute(t, p, f.A, q, held)
			defer f.A.ExecContext(p.Ctx, "ROLLBACK")
			truncateExec(t, f, f.A, "BEGIN", 0)
			truncateExec(t, f, x, q, 0)
			// Mandatory gate: absolutely no intervening sequence function or row read.
			truncateExec(t, f, f.A, "ROLLBACK", 0)
			truncateRestartValue(t, f, f.A, "SELECT nextval('t_id_seq')", "nextval", 5)
			truncateRestartState(t, f, f.A, "t_id_seq", 8, true)
		})
	})
	t.Run("PeerTailRollbackAndCommit", func(t *testing.T) {
		truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
			truncateExec(t, f, f.A, "CREATE TABLE t(id SERIAL)", 0)
			truncateExec(t, f, f.A, "ALTER SEQUENCE t_id_seq CACHE 4", 0)
			truncateRestartValue(t, f, f.B, "SELECT nextval('t_id_seq')", "nextval", 1)
			truncateRestartValue(t, f, f.A, "SELECT nextval('t_id_seq')", "nextval", 5)
			q := "TRUNCATE t RESTART IDENTITY"
			x := truncateRestartRoute(t, p, f.A, q, held)
			defer f.A.ExecContext(p.Ctx, "ROLLBACK")
			truncateExec(t, f, f.A, "BEGIN", 0)
			truncateExec(t, f, x, q, 0)
			truncateExec(t, f, f.A, "ROLLBACK", 0)
			truncateRestartValue(t, f, f.B, "SELECT nextval('t_id_seq')", "nextval", 2)
			truncateRestartValue(t, f, f.A, "SELECT nextval('t_id_seq')", "nextval", 9)
			truncateExec(t, f, x, q, 0)
			// Reset alone preserves observations even when old tails are invalidated.
			truncateRestartValue(t, f, f.A, "SELECT currval('t_id_seq')", "currval", 9)
			truncateRestartValue(t, f, f.B, "SELECT currval('t_id_seq')", "currval", 2)
			truncateRestartValue(t, f, f.B, "SELECT lastval()", "lastval", 2)
			truncateRestartValue(t, f, f.B, "SELECT nextval('t_id_seq')", "nextval", 1)
			truncateRestartValue(t, f, f.A, "SELECT nextval('t_id_seq')", "nextval", 5)
		})
	})
	t.Run("ReplacementDemandRollback", func(t *testing.T) {
		truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
			truncateExec(t, f, f.A, "CREATE TABLE t(id SERIAL)", 0)
			truncateExec(t, f, f.A, "ALTER SEQUENCE t_id_seq CACHE 4", 0)
			truncateRestartValue(t, f, f.A, "SELECT nextval('t_id_seq')", "nextval", 1)
			q := "TRUNCATE t RESTART IDENTITY"
			x := truncateRestartRoute(t, p, f.A, q, held)
			defer f.A.ExecContext(p.Ctx, "ROLLBACK")
			truncateExec(t, f, f.A, "BEGIN", 0)
			truncateExec(t, f, x, q, 0)
			truncateRestartValue(t, f, f.A, "SELECT nextval('t_id_seq')", "nextval", 1)
			truncateRestartValue(t, f, f.A, "SELECT nextval('t_id_seq')", "nextval", 2)
			truncateExec(t, f, f.A, "ROLLBACK", 0)
			truncateRestartValue(t, f, f.A, "SELECT currval('t_id_seq')", "currval", 2)
			truncateRestartValue(t, f, f.A, "SELECT lastval()", "lastval", 2)
			truncateRestartState(t, f, f.B, "t_id_seq", 4, true)
			truncateRestartValue(t, f, f.A, "SELECT nextval('t_id_seq')", "nextval", 5)
		})
	})
	t.Run("FreshSessionMemory", func(t *testing.T) {
		truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
			truncateRestartSeed(t, f)
			q := "TRUNCATE t RESTART IDENTITY"
			x := truncateRestartRoute(t, p, f.B, q, held)
			truncateExec(t, f, x, q, 0)
			truncateRestartValue(t, f, f.A, "SELECT currval('t_id_seq')", "currval", 2)
			truncateRestartValue(t, f, f.A, "SELECT lastval()", "lastval", 2)
			truncateRestartError(t, f, f.B, "SELECT currval('t_id_seq')", truncateWant("55000", `currval of sequence "t_id_seq" is not yet defined in this session`))
			truncateRestartError(t, f, f.B, "SELECT lastval()", truncateWant("55000", "lastval is not yet defined in this session"))
			truncateRestartValue(t, f, f.B, "SELECT nextval('t_id_seq')", "nextval", 1)
		})
	})
}
