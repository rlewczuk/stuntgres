package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteUsingWaitTimeout(t *testing.T) {
	for _, wait := range []string{"target", "source"} {
		for _, explicit := range []bool{false, true} {
			t.Run(wait+map[bool]string{false: "/implicit", true: "/explicit"}[explicit], func(t *testing.T) {
				deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := updateFromRun(t, p, b, c)
					run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
					run(fixture.Exec("CREATE TABLE s(k INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
					run(fixture.Exec("INSERT INTO s VALUES(1),(2)", 2))
					q := "DELETE FROM t d USING s WHERE d.id=s.k RETURNING d.id"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
					pid := 0
					if b.PG {
						pid = fixture.LockPID(t, p.Ctx, c)
						if wait == "target" {
							// Capture the physical tuple independently before mutation;
							// never manufacture native physical Context or copy the error.
							var tuple string
							if err := c.QueryRowContext(p.Ctx, "SELECT ctid::text FROM t WHERE id=2").Scan(&tuple); err != nil {
								t.Fatal(err)
							}
							want.Context = fmt.Sprintf("while deleting tuple %s in relation \"t\"", tuple)
						}
					}
					holder, err := b.DB.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					holderRun := updateFromRun(t, p, b, holder)
					if wait == "target" {
						holderRun(fixture.Exec("DELETE FROM t WHERE id=2", 1))
					} else {
						holderRun(fixture.Exec("ALTER TABLE s ALTER COLUMN k SET NOT NULL", 0))
					}
					run(fixture.Exec("SET statement_timeout='300ms'", 0))
					defer c.ExecContext(p.Ctx, "RESET statement_timeout")
					if explicit {
						run(fixture.Exec("BEGIN", 0))
						run(fixture.Exec("INSERT INTO t VALUES(7,70)", 1))
						defer c.ExecContext(p.Ctx, "ROLLBACK")
					}
					join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						result, err := stmt.ExecContext(ctx)
						return fixture.RelationOutcome{Result: result, Err: err}
					})
					if b.PG {
						updateFromPGWait(t, p.Ctx, pid)
					}
					o := join()
					if o.Result != nil || o.Rows != nil {
						t.Fatal("timeout exposed successful output", o)
					}
					fixture.AssertDiagnostic(t, o.Err, &want)
					if explicit {
						run(fixture.ErrorDiagnostic("SELECT id FROM t", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
						run(fixture.Exec("ROLLBACK", 0))
					}
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					run(fixture.Exec("RESET statement_timeout", 0))
					run(deleteUsingTargets([][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
					run(fixture.Select("SELECT k FROM s ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					run(fixture.Exec("DELETE FROM t d USING s WHERE d.id=s.k", 2))
					run(deleteUsingTargets(nil))
				})
			})
		}
	}
}
