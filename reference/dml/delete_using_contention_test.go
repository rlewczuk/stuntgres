package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteUsingContention(t *testing.T) {
	for _, mode := range []string{"commit", "rollback", "key_change", "delete_commit", "delete_rollback"} {
		for _, prepared := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/direct", true: "/held"}[prepared], func(t *testing.T) {
				deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := updateFromRun(t, p, b, c)
					run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1,10),(9,90)", 2))
					run(fixture.Exec("CREATE TABLE s(k INT,n INT)", 0))
					run(fixture.Exec("INSERT INTO s VALUES(1,101),(1,101)", 2))
					holder, err := b.DB.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					holderRun := updateFromRun(t, p, b, holder)
					if mode == "delete_commit" || mode == "delete_rollback" {
						holderRun(fixture.Exec("DELETE FROM t WHERE id=1", 1))
					} else {
						holderRun(fixture.Exec("UPDATE t SET v=11 WHERE id=1", 1))
					}
					if mode == "key_change" {
						holderRun(fixture.Exec("UPDATE t SET id=2 WHERE id=1", 1))
					}
					q := "DELETE FROM t x USING s WHERE x.id=s.k RETURNING old.v,s.n,new.id"
					var stmt *sql.Stmt
					if prepared {
						stmt, err = c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					pid := 0
					if b.PG {
						pid = fixture.LockPID(t, p.Ctx, c)
					}
					join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						var o fixture.RelationOutcome
						if prepared {
							o.Rows, o.Err = stmt.QueryContext(ctx)
						} else {
							o.Rows, o.Err = c.QueryContext(ctx, q)
						}
						return o
					})
					if b.PG {
						updateFromPGWait(t, p.Ctx, pid)
						peer := updateFromRun(t, p, b, b.DB)
						peer(fixture.Exec("UPDATE s SET n=202", 2))
						peer(fixture.Exec("INSERT INTO s VALUES(1,303),(9,909)", 2))
						// No new originally nonmatching target/source pair can enter.
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
					if o.Err != nil {
						t.Fatal(o.Err)
					}
					old := int64(11)
					if rollback {
						old = 10
					}
					var want [][]any
					if mode != "key_change" && mode != "delete_commit" {
						want = [][]any{{old, int64(101), nil}}
					}
					fixture.DeleteReferenceConsume(t, b.PG, o.Rows, o.Err, fixture.Select(q, []string{"v", "n", "id"}, []string{"INT4", "INT4", "INT4"}, want))
					survivors := [][]any{{int64(9), int64(90)}}
					if mode == "key_change" {
						survivors = append(survivors, []any{int64(2), int64(11)})
					}
					run(deleteUsingTargets(survivors))
				})
			})
		}
	}
}

func TestReferenceDeleteUsingIsolation(t *testing.T) {
	for _, mutation := range []string{"UPDATE t SET v=11 WHERE id=1", "DELETE FROM t WHERE id=1", "UPDATE s SET n=202"} {
		t.Run(mutation, func(t *testing.T) {
			deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
				run(fixture.Exec("CREATE TABLE s(k INT,n INT)", 0))
				run(fixture.Exec("INSERT INTO s VALUES(1,101)", 1))
				run(fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
				updateFromRun(t, p, b, b.DB)(fixture.Exec(mutation, 1))
				q := "DELETE FROM t d USING s WHERE d.id=s.k RETURNING old.v,s.n"
				if mutation == "UPDATE s SET n=202" {
					run(fixture.Select(q, []string{"v", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(101)}}))
				} else {
					message := "could not serialize access due to concurrent update"
					if mutation == "DELETE FROM t WHERE id=1" {
						message = "could not serialize access due to concurrent delete"
					}
					run(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("40001", message)))
				}
				run(fixture.Exec("ROLLBACK", 0))
				var want [][]any
				if mutation == "UPDATE t SET v=11 WHERE id=1" {
					want = [][]any{{int64(1), int64(11)}}
				} else if mutation == "UPDATE s SET n=202" {
					want = [][]any{{int64(1), int64(10)}}
				}
				run(deleteUsingTargets(want))
			})
		})
	}
}
