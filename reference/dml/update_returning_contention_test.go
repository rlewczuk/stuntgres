package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningCurrentPredecessor(t *testing.T) {
	// Native exact pending-resource/journal witnesses live in engine tests.
	// PostgreSQL independently observes the physical transaction-ID blocker.
	for _, lane := range []string{"latest", "no_where", "predicate_false", "peer_delete", "delete_rollback", "rollback", "repeatable_read"} {
		p := fixture.NewScenario(t)
		for _, backend := range p.Backends() {
			t.Run(backend.Name+"/"+lane, func(t *testing.T) {
				a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
				c, err := backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT,v TEXT)", 0)
				fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(2,'old')", 1)
				var ap, bp int
				if backend.PG {
					ap, bp = fixture.LockPID(t, p.Ctx, a), fixture.LockPID(t, p.Ctx, b)
				}
				holder, err := a.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				hold := "UPDATE t SET v='fresh' WHERE id=2"
				if lane == "predicate_false" {
					hold = "UPDATE t SET id=9 WHERE id=2"
				}
				if lane == "peer_delete" || lane == "delete_rollback" {
					hold = "DELETE FROM t WHERE id=2"
				}
				fixture.LockExec(t, p.Ctx, holder, hold, 1)
				var worker *sql.Tx
				if lane == "repeatable_read" {
					worker, err = b.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
					if err != nil {
						t.Fatal(err)
					}
					defer worker.Rollback()
					// Establish the RR snapshot before releasing the holder, even
					// when no public native queue observer is available.
					fixture.RunStep(t, p.Ctx, worker, backend.Recorder, fixture.Select("SELECT v FROM t WHERE id=2", []string{"v"}, []string{"TEXT"}, [][]any{{"old"}}))
				}
				query := "UPDATE t SET v='updated' WHERE id=2 RETURNING v,old.v,new.v"
				if lane == "no_where" {
					query = "UPDATE t SET v='updated' RETURNING v,old.v,new.v"
				}
				await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					if worker != nil {
						rows, err := worker.QueryContext(ctx, query)
						return fixture.RelationOutcome{Rows: rows, Err: err}
					}
					rows, err := b.QueryContext(ctx, query)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				if backend.PG {
					updateFromPGWait(t, p.Ctx, bp)
					var exactBlocker bool
					if err = fixture.Admin.QueryRowContext(p.Ctx, "SELECT $2=ANY(pg_blocking_pids($1))", bp, ap).Scan(&exactBlocker); err != nil || !exactBlocker {
						t.Fatal("unexpected tuple blocker", exactBlocker, err)
					}
					fixture.LockExec(t, p.Ctx, c, "INSERT INTO t VALUES(2,'later')", 1)
				}
				if lane == "rollback" || lane == "delete_rollback" {
					err = holder.Rollback()
				} else {
					err = holder.Commit()
				}
				if err != nil {
					t.Fatal(err)
				}
				o := await()
				if lane == "repeatable_read" {
					if o.Rows != nil {
						o.Rows.Close()
						t.Fatal("serialization failure cursor")
					}
					fixture.AssertDiagnostic(t, o.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to concurrent update"})
					if err = worker.Rollback(); err != nil {
						t.Fatal(err)
					}
					if backend.PG {
						fixture.DeleteReferenceRows(t, p, c, backend.Recorder, 2, 2)
					} else {
						fixture.DeleteReferenceRows(t, p, c, backend.Recorder, 2)
					}
					return
				}
				if o.Err != nil {
					t.Fatal(o.Err)
				}
				defer o.Rows.Close()
				want := "fresh"
				if lane == "rollback" || lane == "delete_rollback" {
					want = "old"
				}
				if lane == "predicate_false" || lane == "peer_delete" {
					if o.Rows.Next() {
						t.Fatal("skipped delete produced output")
					}
				} else {
					if !o.Rows.Next() {
						t.Fatal(o.Rows.Err())
					}
					var unqualified, old string
					var newValue any
					if err = o.Rows.Scan(&unqualified, &old, &newValue); err != nil || unqualified != "updated" || old != want || newValue != "updated" {
						t.Fatal(unqualified, old, newValue, err)
					}
					if o.Rows.Next() {
						t.Fatal("late insert entered fixed candidates")
					}
				}
				if err = o.Rows.Err(); err != nil {
					t.Fatal(err)
				}
				o.Rows.Close()
				if lane == "predicate_false" {
					if backend.PG {
						fixture.DeleteReferenceRows(t, p, c, backend.Recorder, 2, 9)
					} else {
						fixture.DeleteReferenceRows(t, p, c, backend.Recorder, 9)
					}
				} else if lane == "peer_delete" {
					if backend.PG {
						fixture.DeleteReferenceRows(t, p, c, backend.Recorder, 2)
					} else {
						fixture.DeleteReferenceRows(t, p, c, backend.Recorder)
					}
				} else {
					if backend.PG {
						fixture.DeleteReferenceRows(t, p, c, backend.Recorder, 2, 2)
					} else {
						fixture.DeleteReferenceRows(t, p, c, backend.Recorder, 2)
					}
				}
			})
		}
	}
}
