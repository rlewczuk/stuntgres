package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryTableCreateExplicitAdmission(t *testing.T) {
	for _, sourceState := range []string{"missing", "locked"} {
		for _, route := range []string{"direct", "held", "prepare"} {
			t.Run(sourceState+"/"+route, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						a, c := fixture.LockConnections(t, p.Ctx, b.DB)
						run := func(conn *sql.Conn, step fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, step) }
						run(c, fixture.Exec("CREATE TABLE qc_explicit_source(i INT)", 0))
						run(c, fixture.Exec("INSERT INTO qc_explicit_source VALUES(7)", 1))
						q := "CREATE TABLE qc_explicit_target AS SELECT i FROM qc_explicit_source"
						var statement *sql.Stmt
						if route == "held" {
							var err error
							statement, err = c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer statement.Close()
						}
						var blocker *sql.Tx
						if sourceState == "missing" {
							run(c, fixture.Exec("DROP TABLE qc_explicit_source", 0))
						} else {
							var err error
							blocker, err = a.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer blocker.Rollback()
							fixture.RunStep(t, p.Ctx, blocker, b.Recorder, fixture.Exec("LOCK TABLE qc_explicit_source IN ACCESS EXCLUSIVE MODE", 0))
						}
						tx, err := c.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SET LOCAL lock_timeout='100ms'", 0))
						want := fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")
						if b.PG {
							if sourceState == "missing" {
								want = fixture.ControlsReferenceDiagnostic("42P01", `relation "qc_explicit_source" does not exist`)
							} else {
								want = fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
							}
							want.Position = fixture.Position(strings.Index(q, "qc_explicit_source") + 1)
						}
						step := fixture.ErrorDiagnostic(q, want)
						if route == "held" {
							held := tx.StmtContext(p.Ctx, statement)
							defer held.Close()
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: tx, Statement: held, SQL: q}, b.Recorder, step)
						} else {
							if route == "prepare" {
								step.Prepared = true
								step.ErrorStage = "prepare"
							}
							fixture.RunStep(t, p.Ctx, tx, b.Recorder, step)
						}
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
						if err = tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						if blocker != nil {
							if err = blocker.Rollback(); err != nil {
								t.Fatal(err)
							}
						}
						fixture.QueryTableCreateAbsent(t, func(step fixture.Step) { run(c, step) }, "qc_explicit_target")
						run(c, fixture.Select("SELECT 1 AS recovered", []string{"recovered"}, []string{"INT4"}, [][]any{{int64(1)}}))
						if sourceState == "locked" {
							run(c, fixture.Select("SELECT i FROM qc_explicit_source", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
						}
					})
				}
			})
		}
	}
}
