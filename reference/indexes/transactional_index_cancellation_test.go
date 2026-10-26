package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalIndexCancellation(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					a, c := fixture.LockConnections(t, p.Ctx, b.DB)
					runA := transactionalIndexRun(t, p, b, a)
					run := transactionalIndexRun(t, p, b, c)
					runA(fixture.Exec("CREATE TABLE t(n INT)", 0))
					q := "CREATE INDEX idx ON t(n)"
					var e fixture.Executor = c
					if held {
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						e = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					}
					runA(fixture.Exec("BEGIN", 0))
					defer a.ExecContext(p.Ctx, "ROLLBACK")
					runA(fixture.Exec("INSERT INTO t VALUES(1)", 1))
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("SAVEPOINT recover", 0))
					fixture.RelationLockActiveCancel(t, p, b, c, q, func(ctx context.Context) fixture.RelationOutcome {
						r, err := e.ExecContext(ctx, q)
						return fixture.RelationOutcome{Result: r, Err: err}
					})
					run(fixture.ErrorDiagnostic("SELECT 1", transactionalIndexAborted()))
					run(fixture.Exec("ROLLBACK TO recover", 0))
					run(transactionalIndexName("idx", false))
					run(fixture.Exec("SET LOCAL lock_timeout='10ms'", 0))
					s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout"))
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
					run(fixture.ErrorDiagnostic("SELECT 1", transactionalIndexAborted()))
					run(fixture.Exec("ROLLBACK TO recover", 0))
					run(transactionalIndexName("idx", false))
					runA(fixture.Exec("COMMIT", 0))
					fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0))
					run(fixture.Exec("COMMIT", 0))
					runA(fixture.Exec("INSERT INTO t VALUES(2)", 1))
					runA(fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				})
			}
		})
	}
}
