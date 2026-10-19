package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBPathOpsAtomicity(t *testing.T) {
	jsonbPathOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE jpo_input(id int,doc jsonb,path jsonpath)`, 0))
		run(fixture.Exec(`INSERT INTO jpo_input VALUES(1,'true','$'),(2,'true','$v')`, 2))
		run(fixture.Exec(`CREATE TABLE jpo_out(id int,v boolean)`, 0))
		run(fixture.Exec(`INSERT INTO jpo_out VALUES(0,false)`, 1))
		for _, op := range jsonbPathOps {
			t.Run(op, func(t *testing.T) {
				q := `INSERT INTO jpo_out SELECT id,doc ` + op + ` path FROM jpo_input ORDER BY id`
				bad := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42704", `could not find jsonpath variable "v"`))
				bad.ErrorStage = "execute"
				run(bad)
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
				run(fixture.Select(`SELECT id,v FROM jpo_out`, []string{"id", "v"}, []string{"INT4", "BOOL"}, [][]any{{int64(0), false}}))
				ctas := fixture.ErrorDiagnostic(`CREATE TABLE jpo_failed AS SELECT doc `+op+` path AS v FROM jpo_input ORDER BY id`, fixture.ControlsReferenceDiagnostic("42704", `could not find jsonpath variable "v"`))
				run(ctas)
				run(fixture.Prepared(ctas))
				run(fixture.Select(`SELECT count(*) AS absent FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname='jpo_failed'`, []string{"absent"}, []string{"INT8"}, [][]any{{int64(0)}}))
				run(fixture.Exec(`UPDATE jpo_input SET path='$' WHERE id=2`, 1))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2))
				run(fixture.Select(`SELECT id,v FROM jpo_out ORDER BY id`, []string{"id", "v"}, []string{"INT4", "BOOL"}, [][]any{{int64(0), false}, {int64(1), true}, {int64(2), true}}))
				run(fixture.Exec(`DELETE FROM jpo_out WHERE id>0`, 2))
				run(fixture.Exec(`UPDATE jpo_input SET path='strict $.missing'`, 2))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2))
				run(fixture.Select(`SELECT id,v FROM jpo_out ORDER BY id`, []string{"id", "v"}, []string{"INT4", "BOOL"}, [][]any{{int64(0), false}, {int64(1), nil}, {int64(2), nil}}))
				run(fixture.Exec(`DELETE FROM jpo_out WHERE id>0`, 2))
				run(fixture.Exec(`UPDATE jpo_input SET path='$' WHERE id=1`, 1))
				run(fixture.Exec(`UPDATE jpo_input SET path='$v' WHERE id=2`, 1))
			})
		}
	})
}
