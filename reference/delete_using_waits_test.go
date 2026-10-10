package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceDeleteUsingWaitTimeout(t *testing.T) {
	for _, wait := range []string{"target", "source"} {
		for _, explicit := range []bool{false, true} {
			t.Run(wait+map[bool]string{false: "/implicit", true: "/explicit"}[explicit], func(t *testing.T) {
				deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
					run := updateFromRun(t, p, b, c)
					run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
					run(referenceExec("CREATE TABLE s(k INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(1,10),(2,20)", 2))
					run(referenceExec("INSERT INTO s VALUES(1),(2)", 2))
					q := "DELETE FROM t d USING s WHERE d.id=s.k RETURNING d.id"
					stmt, err := c.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					want := controlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
					pid := 0
					if b.pg {
						pid = referenceLockPID(t, p.ctx, c)
						if wait == "target" {
							// Capture the physical tuple independently before mutation;
							// never manufacture native physical Context or copy the error.
							var tuple string
							if err := c.QueryRowContext(p.ctx, "SELECT ctid::text FROM t WHERE id=2").Scan(&tuple); err != nil {
								t.Fatal(err)
							}
							want.Context = fmt.Sprintf("while deleting tuple %s in relation \"t\"", tuple)
						}
					}
					holder, err := b.db.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					holderRun := updateFromRun(t, p, b, holder)
					if wait == "target" {
						holderRun(referenceExec("DELETE FROM t WHERE id=2", 1))
					} else {
						holderRun(referenceExec("ALTER TABLE s ALTER COLUMN k SET NOT NULL", 0))
					}
					run(referenceExec("SET statement_timeout='300ms'", 0))
					defer c.ExecContext(p.ctx, "RESET statement_timeout")
					if explicit {
						run(referenceExec("BEGIN", 0))
						run(referenceExec("INSERT INTO t VALUES(7,70)", 1))
						defer c.ExecContext(p.ctx, "ROLLBACK")
					}
					join := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
						result, err := stmt.ExecContext(ctx)
						return relationOutcome{result: result, err: err}
					})
					if b.pg {
						updateFromPGWait(t, p.ctx, pid)
					}
					o := join()
					if o.result != nil || o.rows != nil {
						t.Fatal("timeout exposed successful output", o)
					}
					referenceAssertDiagnostic(t, o.err, &want)
					if explicit {
						run(referenceErrorDiagnostic("SELECT id FROM t", controlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
						run(referenceExec("ROLLBACK", 0))
					}
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					run(referenceExec("RESET statement_timeout", 0))
					run(deleteUsingTargets([][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
					run(referenceSelect("SELECT k FROM s ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					run(referenceExec("DELETE FROM t d USING s WHERE d.id=s.k", 2))
					run(deleteUsingTargets(nil))
				})
			})
		}
	}
}
