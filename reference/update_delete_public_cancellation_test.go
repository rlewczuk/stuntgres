package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestReferenceUpdateCoreDeleteCancellation(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "delete_wait", true: "update_wait"}[reverse]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
						defer cancel()
						db := backend.db
						holder, owner := referenceLockConnections(t, ctx, db)
						peer, err := db.Conn(ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer peer.Close()
						exec := func(x referenceExecutor, q string, n int64) { updateCoreExec(t, ctx, x, q, false, n) }
						rows := func(q string, want [][]any) {
							step := referenceSelect(q, []string{"id", "v"}, []string{"INT4", "INT4"}, want)
							step.bag = true
							referenceRunStep(t, ctx, peer, backend.recorder, step)
						}
						exec(peer, "CREATE TABLE t(id INT,v INT)", 0)
						exec(peer, "INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3)
						q := "DELETE FROM t WHERE id=3"
						hold := "UPDATE t SET v=99 WHERE id=3"
						if reverse {
							q = "UPDATE t SET v=33 WHERE id=3"
							hold = "DELETE FROM t WHERE id=3"
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
						if backend.pg {
							if err := peer.QueryRowContext(ctx, "SELECT ctid::text FROM t WHERE id=3").Scan(&tuple); err != nil {
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
							cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
							defer stop()
							owner.ExecContext(cleanup, "ROLLBACK")
							if backend.pg {
								owner.ExecContext(cleanup, "SET statement_timeout=0")
							}
						}()
						if reverse {
							exec(owner, "DELETE FROM t WHERE id=1", 1)
							exec(owner, "UPDATE t SET v=22 WHERE id=2", 1)
						} else {
							exec(owner, "UPDATE t SET v=20 WHERE id=1", 1)
							exec(owner, "DELETE FROM t WHERE id=2", 1)
						}
						// Native uses the original ordinary client deadline. PostgreSQL uses its
						// independently specified SQL timeout so the same physical aborted owner survives.
						wait, stop := context.WithTimeout(ctx, 25*time.Millisecond)
						if backend.pg {
							stop()
							wait = ctx
							exec(owner, "SET statement_timeout='25ms'", 0)
						}
						var result sql.Result
						if prepared {
							result, err = stmt.ExecContext(wait)
						} else {
							result, err = owner.ExecContext(wait, q)
						}
						stop()
						if result != nil {
							t.Fatal("canceled update returned result")
						}
						if backend.pg {
							verb := "deleting"
							if reverse {
								verb = "updating"
							}
							referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to statement timeout", Context: "while " + verb + " tuple " + tuple + ` in relation "t"`})
						} else {
							if !errors.Is(err, context.DeadlineExceeded) {
								t.Fatal(err)
							}
							var state interface{ SQLState() string }
							if errors.As(err, &state) {
								t.Fatal("ordinary deadline gained SQLSTATE", err)
							}
						}
						_, err = owner.ExecContext(ctx, "UPDATE t SET v=0")
						referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
						rows("SELECT id,v FROM t", [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}})
						want := [][]any{{int64(1), int64(11)}, {int64(3), int64(30)}}
						if reverse {
							exec(peer, "DELETE FROM t WHERE id=1", 1)
							exec(peer, "UPDATE t SET v=21 WHERE id=2", 1)
							want = [][]any{{int64(2), int64(21)}, {int64(3), int64(30)}}
						} else {
							exec(peer, "UPDATE t SET v=11 WHERE id=1", 1)
							exec(peer, "DELETE FROM t WHERE id=2", 1)
						}
						exec(owner, "ROLLBACK", 0)
						if backend.pg {
							exec(owner, "SET statement_timeout=0", 0)
						}
						if err = h.Rollback(); err != nil {
							t.Fatal(err)
						}
						rows("SELECT id,v FROM t", want)
					})
				}
			})
		}
	}
}
