package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerDDLTransactions(t *testing.T) {
	t.Run("private-chain", func(t *testing.T) {
		triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			triggerDDLChain(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			defer c.ExecContext(context.Background(), "ROLLBACK")
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Prepared(fixture.Exec(`ALTER TRIGGER z ON tr_data RENAME TO "0double"`, 0)))
			run(triggerRows("INSERT INTO tr_data VALUES(1,5) RETURNING id,v", []any{int64(1), int64(13)}))
			run(fixture.Exec("ROLLBACK", 0))
			run(triggerRows("INSERT INTO tr_data VALUES(2,5) RETURNING id,v", []any{int64(2), int64(16)}))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec(`ALTER TRIGGER z ON tr_data RENAME TO "0double"`, 0))
			run(fixture.Exec("COMMIT", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT keep_chain", 0))
			run(fixture.Prepared(fixture.Exec(`DROP TRIGGER "0double" ON tr_data`, 0)))
			run(triggerRows("INSERT INTO tr_data VALUES(3,5) RETURNING id,v", []any{int64(3), int64(8)}))
			run(fixture.Exec("ROLLBACK TO keep_chain", 0))
			run(triggerRows("INSERT INTO tr_data VALUES(4,5) RETURNING id,v", []any{int64(4), int64(13)}))
			run(fixture.Exec("COMMIT", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec(`DROP TRIGGER "0double" ON tr_data`, 0))
			run(fixture.Exec("DROP TRIGGER a ON tr_data", 0))
			run(fixture.Exec("COMMIT", 0))
			run(triggerRows("INSERT INTO tr_data VALUES(5,5) RETURNING id,v", []any{int64(5), int64(5)}))
			run(triggerRows("SELECT id,v FROM tr_data ORDER BY id", []any{int64(2), int64(16)}, []any{int64(4), int64(13)}, []any{int64(5), int64(5)}))
		})
	})
	t.Run("private-edges", func(t *testing.T) {
		triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			triggerSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE EXTENSION unaccent", "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			defer c.ExecContext(context.Background(), "ROLLBACK")
			observer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer observer.Close()
			observe := func(count int64) {
				fixture.RunStep(t, p.Ctx, observer, b.Recorder, triggerDDLX("tr_data", "unaccent", count))
			}
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("ALTER TRIGGER a ON tr_data DEPENDS ON EXTENSION unaccent", 0))
			run(triggerDDLX("tr_data", "unaccent", 1))
			observe(0)
			run(fixture.Exec("SAVEPOINT edges", 0))
			run(fixture.Prepared(fixture.Exec("ALTER TRIGGER a ON tr_data NO DEPENDS ON EXTENSION unaccent", 0)))
			run(triggerDDLX("tr_data", "unaccent", 0))
			run(fixture.Exec("ROLLBACK TO edges", 0))
			run(triggerDDLX("tr_data", "unaccent", 1))
			run(fixture.Exec("ROLLBACK", 0))
			run(triggerDDLX("tr_data", "unaccent", 0))
			run(fixture.Exec("ALTER TRIGGER a ON tr_data DEPENDS ON EXTENSION unaccent", 0))
			observe(1)
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Prepared(fixture.Exec("ALTER TRIGGER a ON tr_data NO DEPENDS ON EXTENSION unaccent", 0)))
			run(triggerDDLX("tr_data", "unaccent", 0))
			observe(1)
			run(fixture.Exec("COMMIT", 0))
			observe(0)
			run(fixture.Exec("DROP EXTENSION unaccent RESTRICT", 0))
			run(triggerRows("INSERT INTO tr_data VALUES(1,5) RETURNING id,v", []any{int64(1), int64(10)}))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("ALTER TRIGGER a ON tr_data RENAME TO renamed", 0))
			run(fixture.Error("DROP TRIGGER missing ON tr_data", "42704"))
			run(fixture.Error("SELECT 1", "25P02"))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Exec("DROP TRIGGER a ON tr_data", 0))
		})
	})
	t.Run("cancel-descendant-admission", func(t *testing.T) {
		triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			triggerDDLPartitionSeed(t, p, b, c)
			holder, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Close()
			defer holder.ExecContext(context.Background(), "ROLLBACK")
			fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
			// A consumed SELECT retains AccessShare until transaction end without
			// widening the separate LOCK TABLE partition capability boundary.
			fixture.RunStep(t, p.Ctx, holder, b.Recorder, triggerRows("SELECT id,v FROM tr_sibling"))
			q := "ALTER TRIGGER a ON tr_parent RENAME TO renamed"
			fixture.RelationLockActiveCancel(t, p, b, c, q, func(ctx context.Context) fixture.RelationOutcome {
				r, e := c.ExecContext(ctx, q)
				return fixture.RelationOutcome{Result: r, Err: e}
			})
			fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER TRIGGER a ON tr_parent RENAME TO retained", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, triggerRows("INSERT INTO tr_parent VALUES(1,5) RETURNING id,v", []any{int64(1), int64(8)}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, triggerRows("INSERT INTO tr_parent VALUES(51,5) RETURNING id,v", []any{int64(51), int64(8)}))
		})
	})
}
