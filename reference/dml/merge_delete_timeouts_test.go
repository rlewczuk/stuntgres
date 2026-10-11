package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeDeleteServerCancellation(t *testing.T) {
	for _, tc := range []struct{ setting, code, message string }{{"lock_timeout", "55P03", "canceling statement due to lock timeout"}, {"statement_timeout", "57014", "canceling statement due to statement timeout"}} {
		for _, hold := range []string{"update", "keyShare"} {
			t.Run(tc.setting+"/"+hold, func(t *testing.T) {
				mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
					run := fixture.MergeRun(t, p, b, prepared)
					run(fixture.Exec("CREATE TABLE t(id INT CONSTRAINT t_key UNIQUE,v INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3))
					run(fixture.Exec("CREATE TABLE source(ord INT,id INT)", 0))
					run(fixture.Exec("INSERT INTO source VALUES(1,1),(2,2),(3,4),(4,3)", 4))
					holder, worker := fixture.LockConnections(t, p.Ctx, b.DB)
					var tuple string
					if b.PG {
						if err := holder.QueryRowContext(p.Ctx, "SELECT ctid::text FROM t WHERE id=3").Scan(&tuple); err != nil {
							t.Fatal(err)
						}
					}
					fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
					if hold == "update" {
						fixture.LockExec(t, p.Ctx, holder, "UPDATE t SET v=99 WHERE id=3", 1)
					} else {
						fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Select("SELECT id FROM t WHERE id=3 FOR KEY SHARE", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}))
					}
					fixture.LockExec(t, p.Ctx, worker, "BEGIN", 0)
					q := "MERGE INTO t USING (SELECT * FROM source ORDER BY ord) s ON t.id<=s.id AND t.id>=s.id WHEN MATCHED AND s.id=2 THEN UPDATE SET v=21 WHEN MATCHED THEN DELETE WHEN NOT MATCHED THEN INSERT VALUES(s.id,40)"
					stmt, err := worker.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					fixture.LockExec(t, p.Ctx, worker, "SET LOCAL "+tc.setting+"='50ms'", 0)
					var out fixture.RelationOutcome
					if prepared {
						out = mergeUpdateOperation(p.Ctx, worker, stmt, q)
					} else {
						out = mergeUpdateOperation(p.Ctx, worker, nil, q)
					}
					if out.Result != nil {
						t.Fatal("timeout exposed result")
					}
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					if b.PG {
						d.Context = "while deleting tuple " + tuple + ` in relation "t"`
					}
					fixture.AssertDiagnostic(t, out.Err, &d)
					_, err = worker.ExecContext(p.Ctx, "UPDATE t SET v=0")
					aborted := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
					fixture.AssertDiagnostic(t, err, &aborted)
					run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}}))
					fixture.LockExec(t, p.Ctx, worker, "ROLLBACK", 0)
					fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
					run(fixture.Exec(q, 4))
					run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(21)}, {int64(4), int64(40)}}))
					if !b.PG {
						ctx, cancel := context.WithCancel(p.Ctx)
						cancel()
						r, err := b.DB.ExecContext(ctx, q)
						if r != nil || !errors.Is(err, context.Canceled) {
							t.Fatal("precancelled delete", r, err)
						}
					}
				})
			})
		}
	}
}
