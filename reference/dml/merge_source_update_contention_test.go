package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// PostgreSQL observes the original blocked command. Native's public API has no
// pending observer: timeout/atomic failure followed by a fresh RC retry is a
// separately specified recovery witness, never proof of frozen-candidate restart.
func TestReferenceMergeSourceUpdateReadCommitted(t *testing.T) {
	for _, lane := range []string{"targetOnlyRetainsClass", "targetOnlyNoWiden", "matchedTwoActions", "matchedTwoActionsFixedSource", "matchedTwoActionsInsertFault", "deleteNullable", "deleteNotNull", "deleteUnique", "deleteDefault", "deleteNoInsert"} {
		for _, commit := range []bool{false, true} {
			t.Run(lane+map[bool]string{false: "Rollback", true: "Commit"}[commit], func(t *testing.T) {
				mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
					ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
					defer cancel()
					holder, worker := fixture.LockConnections(t, ctx, b.DB)
					run := fixture.MergeRun(t, p, b, false)
					ddl := "CREATE TABLE t(id INT,v INT)"
					if lane == "deleteNotNull" {
						ddl = "CREATE TABLE t(id INT NOT NULL,v INT NOT NULL)"
					}
					if lane == "deleteUnique" {
						ddl = "CREATE TABLE t(id INT CONSTRAINT t_key UNIQUE,v INT)"
					}
					if lane == "deleteDefault" {
						ddl = "CREATE TABLE t(id INT DEFAULT 8,v INT DEFAULT 80)"
					}
					run(fixture.Exec(ddl, 0))
					run(fixture.Exec("CREATE TABLE source(id INT,x INT)", 0))
					run(fixture.Exec("INSERT INTO source VALUES(1,7)", 1))
					seed := "(2,10)"
					hold := "UPDATE t SET id=1,v=20 WHERE id=2"
					q := "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=999 WHEN NOT MATCHED BY SOURCE AND t.v>=20 THEN UPDATE SET v=100 WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=200"
					matched := lane == "matchedTwoActions" || lane == "matchedTwoActionsFixedSource" || lane == "matchedTwoActionsInsertFault"
					if matched {
						seed = "(1,10)"
						hold = "UPDATE t SET id=2 WHERE id=1"
						q = "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=1 WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+100 WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,s.x)"
						if lane == "matchedTwoActionsInsertFault" {
							q = "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=1 WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+100 WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,100/(s.id-1))"
						}
					}
					if lane == "targetOnlyNoWiden" {
						q = "MERGE INTO t USING source s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=100"
					}
					deleted := lane == "deleteNullable" || lane == "deleteNotNull" || lane == "deleteUnique" || lane == "deleteDefault" || lane == "deleteNoInsert"
					if deleted {
						hold = "DELETE FROM t WHERE id=2"
						q = "MERGE INTO t USING source s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+100"
						if lane == "deleteDefault" {
							q += " WHEN NOT MATCHED BY TARGET THEN INSERT DEFAULT VALUES"
						} else if lane == "deleteUnique" {
							q += " WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(COALESCE(s.id,1),s.x)"
						} else if lane != "deleteNoInsert" {
							q += " WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,s.x)"
						}
					}
					run(fixture.Exec("INSERT INTO t VALUES"+seed, 1))
					var held *sql.Stmt
					var err error
					if prepared {
						held, err = worker.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
					}
					var hp, wp int
					if b.PG {
						hp, wp = fixture.LockPID(t, ctx, holder), fixture.LockPID(t, ctx, worker)
					}
					fixture.LockExec(t, ctx, holder, "BEGIN", 0)
					fixture.LockExec(t, ctx, holder, hold, 1)
					var await func() fixture.RelationOutcome
					if b.PG {
						await = fixture.LockOperation(t, ctx, func(c context.Context) fixture.RelationOutcome { return mergeUpdateOperation(c, worker, held, q) }, 15*time.Second)
						fixture.RowWait(t, ctx, wp, hp)
					} else {
						mergeUpdateNativeTimeout(t, ctx, worker, held, q, false)
						id := int64(2)
						if matched {
							id = 1
						}
						run(fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{id, int64(10)}}))
						await = func() fixture.RelationOutcome { return mergeUpdateOperation(ctx, worker, held, q) }
					}
					if lane == "matchedTwoActionsFixedSource" {
						run(fixture.Exec("UPDATE source SET x=8", 1))
					}
					if lane == "targetOnlyNoWiden" {
						run(fixture.Exec("INSERT INTO t VALUES(3,30)", 1))
					}
					end := "ROLLBACK"
					if commit {
						end = "COMMIT"
					}
					fixture.LockExec(t, ctx, holder, end, 0)
					out := await()
					n := int64(1)
					want := [][]any{{int64(2), int64(200)}}
					d := fixture.Diagnostic{}
					switch lane {
					case "targetOnlyRetainsClass":
						if commit {
							v := int64(100)
							if !b.PG {
								v = 999
							}
							want = [][]any{{int64(1), v}}
						}
					case "targetOnlyNoWiden":
						want = [][]any{{int64(2), int64(100)}, {int64(3), int64(30)}}
						if commit {
							want = [][]any{{int64(1), int64(100)}, {int64(3), int64(30)}}
						}
						if !b.PG {
							n = 2
							want[1][1] = int64(100)
							if commit {
								n = 1
								want[0][1] = int64(20)
							}
						}
					case "matchedTwoActions", "matchedTwoActionsFixedSource", "matchedTwoActionsInsertFault":
						want = [][]any{{int64(1), int64(1)}}
						if commit {
							n = 2
							x := int64(7)
							if !b.PG && lane == "matchedTwoActionsFixedSource" {
								x = 8
							}
							want = [][]any{{int64(1), x}, {int64(2), int64(110)}}
							if lane == "matchedTwoActionsInsertFault" {
								d = fixture.Diagnostic{Code: "22012", Message: "division by zero"}
								want = [][]any{{int64(2), int64(10)}}
							}
						}
					case "deleteNullable", "deleteNotNull", "deleteUnique":
						n = 2
						want = [][]any{{int64(1), int64(7)}, {int64(2), int64(110)}}
						if commit {
							want = [][]any{{int64(1), int64(7)}, {nil, nil}}
							if !b.PG {
								n = 1
								want = want[:1]
							} else if lane == "deleteNotNull" {
								d = fixture.Diagnostic{Code: "23502", Message: `null value in column "id" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, null).", Schema: "public", Table: "t", Column: "id"}
								want = [][]any{}
							} else if lane == "deleteUnique" {
								d = fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "t_key"`, Detail: "Key (id)=(1) already exists.", Schema: "public", Table: "t", Constraint: "t_key"}
								want = [][]any{}
							}
						}
					case "deleteDefault":
						n = 2
						want = [][]any{{int64(2), int64(110)}, {int64(8), int64(80)}}
						if commit {
							want = [][]any{{int64(8), int64(80)}, {int64(8), int64(80)}}
							if !b.PG {
								n = 1
								want = want[:1]
							}
						}
					case "deleteNoInsert":
						want = [][]any{{int64(2), int64(110)}}
						if commit {
							n = 0
							want = [][]any{}
						}
					}
					if d.Code != "" {
						if out.Result != nil {
							t.Fatal("failed command exposed result")
						}
						d.Severity = "ERROR"
						d.SeverityUnlocalized = "ERROR"
						fixture.AssertDiagnostic(t, out.Err, &d)
					} else {
						mergeInsertAffected(t, b, out.Result, out.Err, n)
					}
					fixture.RunStep(t, ctx, worker, b.Recorder, fixture.Select("SELECT id,v FROM t ORDER BY id NULLS LAST,v NULLS LAST", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
					fixture.LockExec(t, ctx, worker, "UPDATE source SET x=x", 1)
				})
			})
		}
	}
}

// Native timeout recovery preserves this RR transaction's original snapshot and
// prior own write. PostgreSQL additionally observes the original in-flight wait.
func TestReferenceMergeSourceUpdateRepeatableRead(t *testing.T) {
	for _, lane := range []string{"update", "delete", "deleteInsert", "followupUnconditional", "followupOriginalOnly", "followupOwnOnly"} {
		for _, commit := range []bool{false, true} {
			t.Run(lane+map[bool]string{false: "Rollback", true: "Commit"}[commit], func(t *testing.T) {
				mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
					ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
					defer cancel()
					holder, worker := fixture.LockConnections(t, ctx, b.DB)
					run := fixture.MergeRun(t, p, b, false)
					run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(2,10),(9,90)", 2))
					fixture.LockExec(t, ctx, worker, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
					fixture.RunStep(t, ctx, worker, b.Recorder, fixture.Select("SELECT v FROM t WHERE id=2", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
					fixture.LockExec(t, ctx, worker, "UPDATE t SET v=91 WHERE id=9", 1)
					fixture.LockExec(t, ctx, worker, "SAVEPOINT keep", 0)
					hold := "UPDATE t SET v=20 WHERE id=2"
					if lane == "delete" || lane == "deleteInsert" {
						hold = "DELETE FROM t WHERE id=2"
					}
					q := "MERGE INTO t USING (VALUES(1,7)) s(id,x) ON t.id=s.id WHEN NOT MATCHED BY SOURCE AND t.id=2 THEN UPDATE SET v=100"
					if lane == "deleteInsert" {
						q += " WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,s.x)"
					}
					var held *sql.Stmt
					var err error
					if prepared {
						held, err = worker.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
					}
					var hp, wp int
					if b.PG {
						hp, wp = fixture.LockPID(t, ctx, holder), fixture.LockPID(t, ctx, worker)
					}
					fixture.LockExec(t, ctx, holder, "BEGIN", 0)
					fixture.LockExec(t, ctx, holder, hold, 1)
					var await func() fixture.RelationOutcome
					if b.PG {
						await = fixture.LockOperation(t, ctx, func(c context.Context) fixture.RelationOutcome { return mergeUpdateOperation(c, worker, held, q) }, 15*time.Second)
						fixture.RowWait(t, ctx, wp, hp)
					} else {
						mergeUpdateNativeTimeout(t, ctx, worker, held, q, true)
						fixture.RunStep(t, ctx, worker, b.Recorder, fixture.Select("SELECT id,v FROM t ORDER BY id,v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(10)}, {int64(9), int64(91)}}))
						await = func() fixture.RelationOutcome { return mergeUpdateOperation(ctx, worker, held, q) }
					}
					end := "ROLLBACK"
					if commit {
						end = "COMMIT"
					}
					fixture.LockExec(t, ctx, holder, end, 0)
					out := await()
					deleted := lane == "delete" || lane == "deleteInsert"
					if commit && deleted {
						if out.Result != nil {
							t.Fatal("serialization exposed result")
						}
						d := fixture.ControlsReferenceDiagnostic("40001", "could not serialize access due to concurrent delete")
						fixture.AssertDiagnostic(t, out.Err, &d)
						_, err = worker.ExecContext(ctx, "SELECT * FROM t")
						aborted := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
						fixture.AssertDiagnostic(t, err, &aborted)
						fixture.LockExec(t, ctx, worker, "ROLLBACK TO keep", 0)
					} else {
						n := int64(1)
						if lane == "deleteInsert" {
							n = 2
						}
						mergeInsertAffected(t, b, out.Result, out.Err, n)
					}
					bag := func(own int64) {
						want := [][]any{{int64(2), own}, {int64(9), int64(91)}}
						if commit {
							if deleted {
								want = [][]any{{int64(2), int64(10)}, {int64(9), int64(91)}}
							} else {
								want = [][]any{{int64(2), int64(10)}, {int64(2), own}, {int64(9), int64(91)}}
							}
						} else if lane == "deleteInsert" {
							want = append([][]any{{int64(1), int64(7)}}, want...)
						}
						fixture.RunStep(t, ctx, worker, b.Recorder, fixture.Select("SELECT id,v FROM t ORDER BY id,v", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
					}
					bag(100)
					if out.Err == nil && (lane == "followupUnconditional" || lane == "followupOriginalOnly" || lane == "followupOwnOnly") {
						condition := "t.id=2"
						if lane == "followupOriginalOnly" {
							condition += " AND t.v=10"
						}
						if lane == "followupOwnOnly" {
							condition += " AND t.v=100"
						}
						second := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN NOT MATCHED BY SOURCE AND " + condition + " THEN UPDATE SET v=t.v+1"
						fixture.LockExec(t, ctx, worker, "SAVEPOINT followup", 0)
						var r sql.Result
						if prepared {
							stmt, e := worker.PrepareContext(ctx, second)
							if e != nil {
								t.Fatal(e)
							}
							r, err = stmt.ExecContext(ctx)
							if e = stmt.Close(); e != nil {
								t.Fatal(e)
							}
						} else {
							r, err = worker.ExecContext(ctx, second)
						}
						own := int64(100)
						if commit && lane == "followupUnconditional" {
							if r != nil {
								t.Fatal("cardinality exposed result")
							}
							d := mergeUpdateCardinalityDiagnostic()
							d.Severity = "ERROR"
							d.SeverityUnlocalized = "ERROR"
							fixture.AssertDiagnostic(t, err, &d)
							_, err = worker.ExecContext(ctx, "SELECT * FROM t")
							aborted := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
							fixture.AssertDiagnostic(t, err, &aborted)
							fixture.LockExec(t, ctx, worker, "ROLLBACK TO followup", 0)
						} else {
							n := int64(1)
							if lane == "followupOriginalOnly" {
								n = 0
							} else {
								own = 101
							}
							mergeInsertAffected(t, b, r, err, n)
						}
						fixture.LockExec(t, ctx, worker, "RELEASE followup", 0)
						bag(own)
					}
					fixture.LockExec(t, ctx, worker, "RELEASE keep", 0)
					fixture.RunStep(t, ctx, worker, b.Recorder, fixture.Select("SELECT v FROM t WHERE id=9", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
					fixture.LockExec(t, ctx, worker, "UPDATE t SET v=v+1 WHERE id=9", 1)
					fixture.LockExec(t, ctx, worker, "ROLLBACK", 0)
					want := [][]any{{int64(2), int64(10)}, {int64(9), int64(90)}}
					if commit {
						if lane == "delete" || lane == "deleteInsert" {
							want = want[1:]
						} else {
							want[0][1] = int64(20)
						}
					}
					run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
				})
			})
		}
	}
}
