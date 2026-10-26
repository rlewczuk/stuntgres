package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeUpdateServerCancellation(t *testing.T) {
	mergeUpdateServerCancellation(t, false)
}

func TestReferenceMergeSourceUpdateServerCancellation(t *testing.T) {
	mergeUpdateServerCancellation(t, true)
}

func mergeUpdateServerCancellation(t *testing.T, targetOnly bool) {
	for _, tc := range []struct{ setting, code, message string }{
		{"lock_timeout", "55P03", "canceling statement due to lock timeout"},
		{"statement_timeout", "57014", "canceling statement due to statement timeout"},
	} {
		t.Run(tc.setting, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3))
				holder, worker := fixture.LockConnections(t, p.Ctx, b.DB)
				fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
				fixture.LockExec(t, p.Ctx, holder, "UPDATE t SET v=99 WHERE id=3", 1)
				fixture.LockExec(t, p.Ctx, worker, "BEGIN", 0)
				fixture.LockExec(t, p.Ctx, worker, "UPDATE t SET v=11 WHERE id=1", 1)
				if !targetOnly {
					fixture.LockExec(t, p.Ctx, worker, "DELETE FROM t WHERE id=2", 1)
				}
				fixture.LockExec(t, p.Ctx, worker, "INSERT INTO t VALUES(4,40)", 1)
				q := "MERGE INTO t USING (VALUES(3)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=33"
				if targetOnly {
					q = "MERGE INTO t USING (VALUES(0)) s(id) ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=CASE WHEN t.id=3 THEN 33 ELSE t.v+100 END"
				}
				// Prepare before arming statement_timeout: the timed operation is Execute.
				stmt, err := worker.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				fixture.LockExec(t, p.Ctx, worker, "SET LOCAL "+tc.setting+"='"+map[string]string{"lock_timeout": "10ms", "statement_timeout": "50ms"}[tc.setting]+"'", 0)
				var result interface{ RowsAffected() (int64, error) }
				if prepared {
					result, err = stmt.ExecContext(p.Ctx)
				} else {
					result, err = worker.ExecContext(p.Ctx, q)
				}
				if result != nil {
					t.Fatal("deadline returned result")
				}
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				if b.PG {
					got := fixture.ExtractDiagnostic(t, err)
					d.Context = got.Context
					if !strings.HasPrefix(d.Context, "while updating tuple (") || !strings.HasSuffix(d.Context, `) in relation "t"`) {
						t.Fatal("physical timeout context", got)
					}
				}
				fixture.AssertDiagnostic(t, err, &d)
				_, err = worker.ExecContext(p.Ctx, "UPDATE t SET v=0")
				aborted := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
				fixture.AssertDiagnostic(t, err, &aborted)
				run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}}))
				fixture.LockExec(t, p.Ctx, worker, "ROLLBACK", 0)
				fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
				count := int64(1)
				first, second := int64(10), int64(20)
				if targetOnly {
					count, first, second = 3, 110, 120
				}
				run(fixture.Exec(q, count))
				run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), first}, {int64(2), second}, {int64(3), int64(33)}}))
			})
		})
	}
}
