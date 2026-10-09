package reference_test

import (
	"context"
	"database/sql"
	"runtime"
	"testing"
	"time"
)

func updateFromPGWait(t *testing.T, ctx context.Context, pid int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		var waiting bool
		if err := referenceAdmin.QueryRowContext(ctx, "SELECT cardinality(pg_blocking_pids($1))>0 AND EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND NOT granted)", pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		runtime.Gosched()
	}
}

func TestReferenceUpdateFromContention(t *testing.T) {
	for _, mode := range []string{"commit", "rollback", "key_change", "delete_commit", "delete_rollback"} {
		for _, prepared := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
					run := updateFromRun(t, p, b, c)
					run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(1,10)", 1))
					run(referenceExec("CREATE TABLE s(k INT,n INT)", 0))
					run(referenceExec("INSERT INTO s VALUES(1,101)", 1))
					holder, err := b.db.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					holderRun := updateFromRun(t, p, b, holder)
					if mode == "delete_commit" || mode == "delete_rollback" {
						holderRun(referenceExec("DELETE FROM t WHERE id=1", 1))
					} else {
						holderRun(referenceExec("UPDATE t SET v=11 WHERE id=1", 1))
					}
					if mode == "key_change" {
						holderRun(referenceExec("UPDATE t SET id=2 WHERE id=1", 1))
					}
					q := "UPDATE t x SET v=x.v+s.n FROM s WHERE x.id=s.k RETURNING old.v,s.n,new.v"
					var stmt *sql.Stmt
					if prepared {
						stmt, err = c.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					pid := 0
					if b.pg {
						pid = referenceLockPID(t, p.ctx, c)
					}
					join := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
						var o relationOutcome
						if prepared {
							o.rows, o.err = stmt.QueryContext(ctx)
						} else {
							o.rows, o.err = c.QueryContext(ctx, q)
						}
						return o
					})
					if b.pg {
						updateFromPGWait(t, p.ctx, pid)
						// Only PG has the public queue barrier. Precise native captured-source
						// ordering is proved by the production-round package-function companion.
						peerRun := updateFromRun(t, p, b, b.db)
						peerRun(referenceExec("UPDATE s SET n=202 WHERE k=1", 1))
						peerRun(referenceExec("INSERT INTO s VALUES(1,303)", 1))
					}
					rollback := mode == "rollback" || mode == "delete_rollback"
					if rollback {
						err = holder.Rollback()
					} else {
						err = holder.Commit()
					}
					if err != nil {
						t.Fatal(err)
					}
					o := join()
					if o.err != nil {
						t.Fatal(o.err)
					}
					defer o.rows.Close()
					expectedCount := 1
					if mode == "key_change" || mode == "delete_commit" {
						expectedCount = 0
					}
					old := int64(11)
					if rollback {
						old = 10
					}
					count := 0
					for o.rows.Next() {
						var actualOld, source, next int64
						if err := o.rows.Scan(&actualOld, &source, &next); err != nil {
							t.Fatal(err)
						}
						if actualOld != old || source != 101 || next != old+101 {
							t.Fatal(actualOld, source, next)
						}
						count++
					}
					if err := o.rows.Err(); err != nil {
						t.Fatal(err)
					}
					o.rows.Close()
					if count != expectedCount {
						t.Fatal(count, expectedCount)
					}
				})
			})
		}
	}
}

func TestReferenceUpdateFromIsolation(t *testing.T) {
	for _, mutation := range []string{"UPDATE t SET v=11 WHERE id=1", "DELETE FROM t WHERE id=1"} {
		t.Run(mutation, func(t *testing.T) {
			updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1,10)", 1))
				run(referenceExec("CREATE TABLE s(k INT,n INT)", 0))
				run(referenceExec("INSERT INTO s VALUES(1,101)", 1))
				run(referenceExec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
				defer c.ExecContext(p.ctx, "ROLLBACK")
				run(referenceSelect("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
				peer := updateFromRun(t, p, b, b.db)
				peer(referenceExec(mutation, 1))
				peer(referenceExec("UPDATE s SET n=202", 1))
				want := controlsReferenceDiagnostic("40001", "could not serialize access due to concurrent update")
				if mutation == "DELETE FROM t WHERE id=1" {
					want.Message = "could not serialize access due to concurrent delete"
				}
				updateFromDiagnostic(t, p.ctx, c, "UPDATE t x SET v=s.n FROM s WHERE x.id=s.k", want, false)
				run(referenceExec("ROLLBACK", 0))
			})
		})
	}
}
