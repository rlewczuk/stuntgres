package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExplainAnalyzeErrors(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }
					run(fixture.Exec("CREATE TABLE ea_t(id integer)",0))
					run(fixture.Exec("INSERT INTO ea_t VALUES(1),(2)",2))
					run(fixture.Exec("CREATE TABLE ea_nn(v integer NOT NULL)",0))
					run(fixture.Exec("INSERT INTO ea_nn VALUES(7)",1))
					constraint := fixture.ControlsReferenceDiagnostic("23502",`null value in column "v" of relation "ea_nn" violates not-null constraint`)
					constraint.Detail="Failing row contains (null)."
					constraint.Schema,constraint.Table,constraint.Column="public","ea_nn","v"
					cases:=[]struct{ query string; diagnostic fixture.Diagnostic }{
						{"EXPLAIN ANALYZE SELECT 10/(id-id) FROM ea_t",fixture.ControlsReferenceDiagnostic("22012","division by zero")},
						{"EXPLAIN ANALYZE SELECT (SELECT id FROM ea_t)",fixture.ControlsReferenceDiagnostic("21000","more than one row returned by a subquery used as an expression")},
						{"EXPLAIN ANALYZE INSERT INTO ea_nn VALUES(8),(NULL)",constraint},
						{"EXPLAIN ANALYZE INSERT INTO ea_nn VALUES(8) RETURNING 10/(v-v)",fixture.ControlsReferenceDiagnostic("22012","division by zero")},
					}
					for _,c:=range cases {
						// Preparation must independently succeed: these are runtime
						// failures, not permissive prepare-or-execute expectations.
						if prepared {
							statement,err:=b.DB.PrepareContext(p.Ctx,c.query)
							if err!=nil {t.Fatalf("runtime witness failed Prepare: %v",err)}
							statement.Close()
						}
						s:=fixture.ErrorDiagnostic(c.query,c.diagnostic)
						s.Prepared,s.SelectRows,s.ErrorStage=prepared,true,"execute"
						run(s)
						run(fixture.Select("SELECT v FROM ea_nn ORDER BY v",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
					}
					tx,err:=b.DB.BeginTx(p.Ctx,nil)
					if err!=nil {t.Fatal(err)}
					defer tx.Rollback()
					fixture.RunStep(t,p.Ctx,tx,b.Recorder,fixture.Exec("INSERT INTO ea_nn VALUES(9)",1))
					s:=fixture.ErrorDiagnostic("EXPLAIN ANALYZE INSERT INTO ea_nn VALUES(NULL)",constraint)
					s.Prepared,s.SelectRows,s.ErrorStage=prepared,true,"execute"
					fixture.RunStep(t,p.Ctx,tx,b.Recorder,s)
					fixture.RunStep(t,p.Ctx,tx,b.Recorder,fixture.ErrorDiagnostic("SELECT 1",fixture.ControlsReferenceDiagnostic("25P02","current transaction is aborted, commands ignored until end of transaction block")))
					if err:=tx.Rollback();err!=nil {t.Fatal(err)}
					run(fixture.Select("SELECT v FROM ea_nn ORDER BY v",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
				})
			}
		})
	}
}
