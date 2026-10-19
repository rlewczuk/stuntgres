package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceUpdateRowCancellation(t *testing.T) {
	for _, sourceWait := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "target_wait", true: "source_wait"}[sourceWait]+map[bool]string{false: "/direct", true: "/held"}[prepared], func(t *testing.T) {
				updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					ctx, stop := context.WithTimeout(p.Ctx, 5*time.Second)
					defer stop()
					holder, owner := fixture.LockConnections(t, ctx, b.DB)
					peer, err := b.DB.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					exec := func(x fixture.Executor, q string, n int64) { fixture.UpdateCoreExec(t, ctx, x, q, false, n) }
					exec(peer, "CREATE TABLE t(id INT,a INT,b INT)", 0)
					exec(peer, "INSERT INTO t VALUES(1,10,20),(2,30,40)", 2)
					exec(peer, "CREATE TABLE s(id INT,x INT,y INT)", 0)
					exec(peer, "INSERT INTO s VALUES(1,100,200),(2,300,400)", 2)
					q := "UPDATE t SET (a,b)=(SELECT x,y FROM s WHERE s.id=t.id) WHERE id=2 RETURNING a,b"
					hold, relation, verb := "UPDATE t SET a=99 WHERE id=2", "t", "updating"
					if sourceWait {
						q = "UPDATE t SET (a,b)=(SELECT x,y FROM s WHERE s.id=t.id FOR UPDATE) WHERE id=2 RETURNING a,b"
						hold, relation, verb = "UPDATE s SET x=999 WHERE id=2", "s", "locking"
					}
					var stmt *sql.Stmt
					if prepared {
						stmt, err = owner.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					var tuple string
					if b.PG {
						if err := peer.QueryRowContext(ctx, "SELECT ctid::text FROM "+relation+" WHERE id=2").Scan(&tuple); err != nil {
							t.Fatal(err)
						}
					}
					h, err := holder.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer h.Rollback()
					exec(h, hold, 1)
					exec(owner, "BEGIN", 0)
					defer func() {
						cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						owner.ExecContext(cleanup, "ROLLBACK")
						if b.PG {
							owner.ExecContext(cleanup, "SET statement_timeout=0")
						}
					}()
					exec(owner, "UPDATE t SET (a,b)=ROW(11,21) WHERE id=1", 1)
					wait, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
					if b.PG {
						cancel()
						wait = ctx
						exec(owner, "SET statement_timeout='25ms'", 0)
					}
					var rows *sql.Rows
					if prepared {
						rows, err = stmt.QueryContext(wait)
					} else {
						rows, err = owner.QueryContext(wait, q)
					}
					cancel()
					if rows != nil {
						rows.Close()
						t.Fatal("canceled group exposed RETURNING")
					}
					if b.PG {
						fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to statement timeout", Context: "while " + verb + " tuple " + tuple + ` in relation "` + relation + `"`})
					} else {
						if !errors.Is(err, context.DeadlineExceeded) {
							t.Fatal(err)
						}
						var state interface{ SQLState() string }
						if errors.As(err, &state) {
							t.Fatal("context cancellation gained SQLSTATE", err)
						}
					}
					_, err = owner.ExecContext(ctx, "UPDATE t SET a=0")
					want := fixture.TransactionAborted()
					fixture.AssertDiagnostic(t, err, &want)
					fixture.RunStep(t, ctx, peer, b.Recorder, updateRowSnapshot([][]any{{int64(1), int64(10), int64(20)}, {int64(2), int64(30), int64(40)}}))
					exec(owner, "ROLLBACK", 0)
					if b.PG {
						exec(owner, "SET statement_timeout=0", 0)
					}
					if err := h.Rollback(); err != nil {
						t.Fatal(err)
					}
					// Peer progress proves canceled owner released its earlier row locks.
					exec(peer, "UPDATE t SET (a,b)=ROW(12,22) WHERE id=1", 1)
					if prepared {
						rows, err = stmt.QueryContext(ctx)
					} else {
						rows, err = owner.QueryContext(ctx, q)
					}
					if err != nil {
						t.Fatal(err)
					}
					fixture.UpdateCoreRetained(t, b.PG, rows, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(300), int64(400)}}, false)
					fixture.RunStep(t, ctx, peer, b.Recorder, updateRowSnapshot([][]any{{int64(1), int64(12), int64(22)}, {int64(2), int64(300), int64(400)}}))
				})
			})
		}
	}
}
