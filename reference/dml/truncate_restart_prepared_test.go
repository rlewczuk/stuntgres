package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTruncateRestartPrepared(t *testing.T) {
	t.Run("PrepareBeforeCreation", func(t *testing.T) {
		truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
			q := "TRUNCATE t RESTART IDENTITY"
			x := truncateRestartRoute(t, p, f.A, q, held)
			truncateRestartSeed(t, f)
			truncateRows(t, f, f.A, "t", 1, 2)
			truncateRestartState(t, f, f.A, "t_id_seq", 2, true)
			defer f.A.ExecContext(p.Ctx, "ROLLBACK")
			truncateExec(t, f, f.A, "BEGIN", 0)
			truncateExec(t, f, x, q, 0)
			truncateExec(t, f, f.A, "ROLLBACK", 0)
			truncateRows(t, f, f.A, "t", 1, 2)
			truncateExec(t, f, x, q, 0)
			truncateExec(t, f, f.A, "INSERT INTO t DEFAULT VALUES", 1)
			truncateRows(t, f, f.A, "t", 1)
		})
	})
	for _, action := range []string{"CONTINUE IDENTITY", "RESTART IDENTITY", "RollbackRestart", "RollbackRestartDiscardSequences"} {
		t.Run("Shadow/"+action, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "restart_shadow_a", "restart_shadow_b")
			truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
				for _, q := range []string{
					"CREATE SCHEMA restart_shadow_a", "CREATE SCHEMA restart_shadow_b",
					"CREATE TABLE restart_shadow_b.t(id INT)",
					"CREATE SEQUENCE restart_shadow_b.s START 10 OWNED BY restart_shadow_b.t.id",
					"CREATE SEQUENCE restart_shadow_b.other START 20",
					"SET search_path TO restart_shadow_a,restart_shadow_b",
				} {
					truncateExec(t, f, f.A, q, 0)
				}
				query := "SELECT nextval('s') AS first,nextval('other') AS second"
				// Always hold the consumer: this tests the whole preexisting literal group.
				consumer := truncateRestartRoute(t, p, f.A, query, true)
				truncateRun(t, f, consumer, fixture.Select(query, []string{"first", "second"}, []string{"INT8", "INT8"}, [][]any{{int64(10), int64(20)}}))
				truncateExec(t, f, f.A, "CREATE SEQUENCE restart_shadow_a.s START 100", 0)
				truncateExec(t, f, f.A, "CREATE SEQUENCE restart_shadow_a.other START 200", 0)
				// Creating shadows alone is not an invalidation event.
				truncateRun(t, f, consumer, fixture.Select(query, []string{"first", "second"}, []string{"INT8", "INT8"}, [][]any{{int64(11), int64(21)}}))
				q := "TRUNCATE restart_shadow_b.t " + action
				rollback := action == "RollbackRestart" || action == "RollbackRestartDiscardSequences"
				if rollback {
					q = "TRUNCATE restart_shadow_b.t RESTART IDENTITY"
				}
				x := truncateRestartRoute(t, p, f.A, q, held)
				defer f.A.ExecContext(p.Ctx, "ROLLBACK")
				if rollback {
					truncateExec(t, f, f.A, "BEGIN", 0)
				}
				truncateExec(t, f, x, q, 0)
				if rollback {
					truncateExec(t, f, f.A, "ROLLBACK", 0)
				}
				if action == "RollbackRestartDiscardSequences" {
					truncateExec(t, f, f.A, "DISCARD SEQUENCES", 0)
					truncateRestartError(t, f, f.A, "SELECT currval('restart_shadow_b.s')", truncateWant("55000", `currval of sequence "s" is not yet defined in this session`))
					truncateRestartError(t, f, f.A, "SELECT lastval()", truncateWant("55000", "lastval is not yet defined in this session"))
				}
				first, second := int64(100), int64(200)
				if action == "CONTINUE IDENTITY" {
					first, second = 12, 22
				}
				// Pinned PostgreSQL: rewrite/rollback invalidates the entire group.
				truncateRun(t, f, consumer, fixture.Select(query, []string{"first", "second"}, []string{"INT8", "INT8"}, [][]any{{first, second}}))
				original := int64(10)
				if action == "CONTINUE IDENTITY" {
					original = 13
				}
				if rollback {
					original = 12
				}
				truncateRestartValue(t, f, f.A, "SELECT nextval('restart_shadow_b.s')", "nextval", original)
				truncateExec(t, f, f.A, "SET search_path TO public", 0)
				truncateExec(t, f, f.A, "DROP SCHEMA restart_shadow_a,restart_shadow_b CASCADE", 0)
				*f.Notices = nil // Schema teardown notices are not TRUNCATE notices.
			})
		})
	}
	t.Run("ReadOnlyAndAbortedAdmission", func(t *testing.T) {
		truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
			truncateRestartSeed(t, f)
			for _, name := range []string{"t", "missing"} {
				tx := truncateBegin(t, p, f.A, &sql.TxOptions{ReadOnly: true})
				q := "TRUNCATE " + name + " RESTART IDENTITY"
				x := truncateRestartRoute(t, p, tx, q, held)
				truncateRestartError(t, f, x, q, truncateWant("25006", "cannot execute TRUNCATE TABLE in a read-only transaction"))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			truncateRows(t, f, f.A, "t", 1, 2)
			truncateRestartState(t, f, f.A, "t_id_seq", 2, true)
		})
	})
}
