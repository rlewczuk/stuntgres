package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceQueryTableCreateExplicitAdmission(t *testing.T) {
	for _, sourceState := range []string{"missing", "locked"} {
		for _, route := range []string{"direct", "held", "prepare"} {
			t.Run(sourceState+"/"+route, func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						a, c := referenceLockConnections(t, p.ctx, b.db)
						run := func(conn *sql.Conn, step referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, step) }
						run(c, referenceExec("CREATE TABLE qc_explicit_source(i INT)", 0))
						run(c, referenceExec("INSERT INTO qc_explicit_source VALUES(7)", 1))
						q := "CREATE TABLE qc_explicit_target AS SELECT i FROM qc_explicit_source"
						var statement *sql.Stmt
						if route == "held" {
							var err error
							statement, err = c.PrepareContext(p.ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer statement.Close()
						}
						var blocker *sql.Tx
						if sourceState == "missing" {
							run(c, referenceExec("DROP TABLE qc_explicit_source", 0))
						} else {
							var err error
							blocker, err = a.BeginTx(p.ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer blocker.Rollback()
							referenceRunStep(t, p.ctx, blocker, b.recorder, referenceExec("LOCK TABLE qc_explicit_source IN ACCESS EXCLUSIVE MODE", 0))
						}
						tx, err := c.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("SET LOCAL lock_timeout='100ms'", 0))
						want := controlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")
						if b.pg {
							if sourceState == "missing" {
								want = controlsReferenceDiagnostic("42P01", `relation "qc_explicit_source" does not exist`)
							} else {
								want = controlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
							}
							want.Position = referencePosition(strings.Index(q, "qc_explicit_source") + 1)
						}
						step := referenceErrorDiagnostic(q, want)
						if route == "held" {
							held := tx.StmtContext(p.ctx, statement)
							defer held.Close()
							referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: tx, statement: held, query: q}, b.recorder, step)
						} else {
							if route == "prepare" {
								step.prepared = true
								step.errorStage = "prepare"
							}
							referenceRunStep(t, p.ctx, tx, b.recorder, step)
						}
						referenceRunStep(t, p.ctx, tx, b.recorder, referenceErrorDiagnostic("SELECT 1", controlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
						if err = tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						if blocker != nil {
							if err = blocker.Rollback(); err != nil {
								t.Fatal(err)
							}
						}
						queryTableCreateAbsent(t, func(step referenceStep) { run(c, step) }, b.pg, "qc_explicit_target")
						run(c, referenceSelect("SELECT 1 AS recovered", []string{"recovered"}, []string{"INT4"}, [][]any{{int64(1)}}))
						if sourceState == "locked" {
							run(c, referenceSelect("SELECT i FROM qc_explicit_source", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
						}
					})
				}
			})
		}
	}
}
