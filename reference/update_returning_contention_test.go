package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestReferenceUpdateReturningCurrentPredecessor(t *testing.T) {
	// Native exact pending-resource/journal witnesses live in engine tests.
	// PostgreSQL independently observes the physical transaction-ID blocker.
	for _, lane := range []string{"latest", "no_where", "predicate_false", "peer_delete", "delete_rollback", "rollback", "repeatable_read"} {
		p := newReferenceScenario(t)
		for _, backend := range p.backends() {
			t.Run(backend.name+"/"+lane, func(t *testing.T) {
				a, b := referenceLockConnections(t, p.ctx, backend.db)
				c, err := backend.db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v TEXT)", 0)
				referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(2,'old')", 1)
				var ap, bp int
				if backend.pg {
					ap, bp = referenceLockPID(t, p.ctx, a), referenceLockPID(t, p.ctx, b)
				}
				holder, err := a.BeginTx(p.ctx, nil)
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
				referenceLockExec(t, p.ctx, holder, hold, 1)
				var worker *sql.Tx
				if lane == "repeatable_read" {
					worker, err = b.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
					if err != nil {
						t.Fatal(err)
					}
					defer worker.Rollback()
					// Establish the RR snapshot before releasing the holder, even
					// when no public native queue observer is available.
					referenceRunStep(t, p.ctx, worker, backend.recorder, referenceSelect("SELECT v FROM t WHERE id=2", []string{"v"}, []string{"TEXT"}, [][]any{{"old"}}))
				}
				query := "UPDATE t SET v='updated' WHERE id=2 RETURNING v,old.v,new.v"
				if lane == "no_where" {
					query = "UPDATE t SET v='updated' RETURNING v,old.v,new.v"
				}
				await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
					if worker != nil {
						rows, err := worker.QueryContext(ctx, query)
						return relationOutcome{rows: rows, err: err}
					}
					rows, err := b.QueryContext(ctx, query)
					return relationOutcome{rows: rows, err: err}
				})
				if backend.pg {
					updateFromPGWait(t, p.ctx, bp)
					var exactBlocker bool
					if err = referenceAdmin.QueryRowContext(p.ctx, "SELECT $2=ANY(pg_blocking_pids($1))", bp, ap).Scan(&exactBlocker); err != nil || !exactBlocker {
						t.Fatal("unexpected tuple blocker", exactBlocker, err)
					}
					referenceLockExec(t, p.ctx, c, "INSERT INTO t VALUES(2,'later')", 1)
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
					if o.rows != nil {
						o.rows.Close()
						t.Fatal("serialization failure cursor")
					}
					referenceAssertDiagnostic(t, o.err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to concurrent update"})
					if err = worker.Rollback(); err != nil {
						t.Fatal(err)
					}
					if backend.pg {
						deleteReferenceRows(t, p, c, backend.recorder, 2, 2)
					} else {
						deleteReferenceRows(t, p, c, backend.recorder, 2)
					}
					return
				}
				if o.err != nil {
					t.Fatal(o.err)
				}
				defer o.rows.Close()
				want := "fresh"
				if lane == "rollback" || lane == "delete_rollback" {
					want = "old"
				}
				if lane == "predicate_false" || lane == "peer_delete" {
					if o.rows.Next() {
						t.Fatal("skipped delete produced output")
					}
				} else {
					if !o.rows.Next() {
						t.Fatal(o.rows.Err())
					}
					var unqualified, old string
					var newValue any
					if err = o.rows.Scan(&unqualified, &old, &newValue); err != nil || unqualified != "updated" || old != want || newValue != "updated" {
						t.Fatal(unqualified, old, newValue, err)
					}
					if o.rows.Next() {
						t.Fatal("late insert entered fixed candidates")
					}
				}
				if err = o.rows.Err(); err != nil {
					t.Fatal(err)
				}
				o.rows.Close()
				if lane == "predicate_false" {
					if backend.pg {
						deleteReferenceRows(t, p, c, backend.recorder, 2, 9)
					} else {
						deleteReferenceRows(t, p, c, backend.recorder, 9)
					}
				} else if lane == "peer_delete" {
					if backend.pg {
						deleteReferenceRows(t, p, c, backend.recorder, 2)
					} else {
						deleteReferenceRows(t, p, c, backend.recorder)
					}
				} else {
					if backend.pg {
						deleteReferenceRows(t, p, c, backend.recorder, 2, 2)
					} else {
						deleteReferenceRows(t, p, c, backend.recorder, 2)
					}
				}
			})
		}
	}
}
