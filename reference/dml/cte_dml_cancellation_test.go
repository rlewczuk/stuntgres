package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceCTEDMLCancellation(t *testing.T) {
	for _, queryRows := range []bool{false, true} {
		for _, savepoint := range []bool{false, true} {
			t.Run(map[bool]string{false: "exec", true: "query"}[queryRows]+map[bool]string{false: "/implicit", true: "/savepoint"}[savepoint], func(t *testing.T) {
				cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
					a, worker := fixture.LockConnections(t, p.Ctx, b.DB)
					observer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer observer.Close()
					for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(id INT,v INT)", 0), fixture.Exec("CREATE TABLE audit(id INT)", 0), fixture.Exec("CREATE SEQUENCE s", 0), fixture.Exec("INSERT INTO t VALUES(1,10)", 1)} {
						fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
					}
					q := "WITH a AS (INSERT INTO audit VALUES(1) RETURNING nextval('s') AS token), b AS (UPDATE t SET v=t.v+1 FROM a WHERE t.id=1 AND a.token>0 RETURNING t.id,t.v) SELECT * FROM b"
					var x fixture.Executor = worker
					var tx *sql.Tx
					if savepoint {
						tx, err = worker.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						fixture.LockExec(t, p.Ctx, tx, "INSERT INTO audit VALUES(9)", 1)
						fixture.LockExec(t, p.Ctx, tx, "SAVEPOINT kept", 0)
						x = tx
					}
					if prepared {
						stmt, e := x.PrepareContext(p.Ctx, q)
						if e != nil {
							t.Fatal(e)
						}
						defer stmt.Close()
						x = fixture.HeldStatement{Executor: x, Statement: stmt, SQL: q}
					}
					var ap, bp int
					if b.PG {
						ap, bp = fixture.RowPIDs(t, p.Ctx, a, worker)
					}
					holder, err := a.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					fixture.LockExec(t, p.Ctx, holder, "UPDATE t SET v=99 WHERE id=1", 1)
					ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
					defer cancel()
					done := fixture.RowAsync(t, ctx, x, q, false, queryRows)
					fixture.LimitSequenceCapture(t, ctx, holder)
					if b.PG {
						fixture.RowWait(t, ctx, bp, ap)
					}
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT id FROM audit", []string{"id"}, []string{"INT4"}, [][]any{}))
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
					if b.PG {
						var canceled bool
						if err := observer.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
							t.Fatal(canceled, err)
						}
					} else {
						cancel()
					}
					o := fixture.LockResult(t, done)
					if o.Rows != nil {
						o.Rows.Close()
						t.Fatal("cancellation returned successful rows")
					}
					if o.Result != nil {
						t.Fatal("cancellation returned successful result")
					}
					if b.PG {
						want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
						var tuple string
						if err := observer.QueryRowContext(p.Ctx, "SELECT ctid::text FROM t WHERE id=1").Scan(&tuple); err != nil {
							t.Fatal(err)
						}
						want.Context = "while updating tuple " + tuple + ` in relation "t"`
						fixture.AssertDiagnostic(t, o.Err, &want)
					} else if !errors.Is(o.Err, context.Canceled) {
						t.Fatalf("cancel error %v", o.Err)
					}
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					if savepoint {
						fixture.LockExec(t, p.Ctx, tx, "ROLLBACK TO SAVEPOINT kept", 0)
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT id FROM audit", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
						if err := tx.Commit(); err != nil {
							t.Fatal(err)
						}
					} else {
						// Server cancellation preserves the same PostgreSQL physical owner.
						fixture.RunStep(t, p.Ctx, worker, b.Recorder, fixture.Select("SELECT 7 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}))
					}
					wantAudit := [][]any{}
					if savepoint {
						wantAudit = [][]any{{int64(9)}}
					}
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT id FROM audit", []string{"id"}, []string{"INT4"}, wantAudit))
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT is_called FROM s", []string{"is_called"}, []string{"BOOL"}, [][]any{{true}}))
				})
			})
		}
	}
	t.Run("pre_and_post_publication", func(t *testing.T) {
		cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			cteDMLSeed(t, p, b)
			q := "WITH u AS (UPDATE t SET v=v+1 RETURNING id,v) SELECT * FROM u ORDER BY id"
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				rows, err := b.DB.QueryContext(ctx, q)
				if rows != nil {
					rows.Close()
					t.Fatal("precanceled rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				cteDMLRun(t, p, b, false, fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
			}
			cteDMLRun(t, p, b, prepared, fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(2), int64(21)}}))
			// Closing successful eager output does not undo publication.
			cteDMLRun(t, p, b, false, fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(2), int64(21)}}))
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				var x fixture.Executor = b.DB
				if prepared {
					stmt, err := b.DB.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					x = fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
				}
				rows, err := x.QueryContext(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				cancel()
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				cteDMLRun(t, p, b, false, fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(12)}, {int64(2), int64(22)}}))
			}
		})
	})
}
