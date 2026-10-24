package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func transactionalSchemaReadOnlyAnalysis(t *testing.T) {
	for _, q := range []string{"CREATE TABLE tx_ro_output AS SELECT missing FROM tx_ro_source", "SELECT missing INTO tx_ro_output FROM tx_ro_source"} {
		for _, prepare := range []bool{false, true} {
			for _, goTx := range []bool{false, true} {
				t.Run(fmt.Sprint("source_analysis/", q, "/prepare=", prepare, "/go=", goTx), func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
							fixture.LockExec(t, p.Ctx, c, "CREATE TABLE tx_ro_source(v INT)", 0)
							fixture.LockExec(t, p.Ctx, c, "INSERT INTO tx_ro_source VALUES(7)", 1)
							var e fixture.Executor = c
							rollback := func() { fixture.LockExec(t, p.Ctx, c, "ROLLBACK", 0) }
							if goTx {
								tx, err := c.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
								if err != nil {
									t.Fatal(err)
								}
								defer tx.Rollback()
								e = tx
								rollback = func() {
									if err := tx.Rollback(); err != nil {
										t.Fatal(err)
									}
								}
							} else {
								fixture.LockExec(t, p.Ctx, c, "BEGIN READ ONLY", 0)
								defer c.ExecContext(p.Ctx, "ROLLBACK")
							}
							d := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
							d.Position = fixture.Position(strings.Index(q, "missing") + 1)
							s := fixture.ErrorDiagnostic(q, d)
							s.Prepared = prepare
							s.ErrorStage = "execute"
							if prepare {
								s.ErrorStage = "prepare"
							}
							fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
							fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
							rollback()
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tx_ro_output'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM tx_ro_source", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
						})
					}
				})
			}
		}
	}
}
