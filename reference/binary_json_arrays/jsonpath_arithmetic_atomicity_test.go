package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathArithmeticAtomicity(t *testing.T) {
	for _, failure := range []struct{ path, code, message string }{
		{`$ / 0`, "22012", `division by zero`},
		{`$ + "x"`, "22038", `right operand of jsonpath operator + is not a single numeric value`},
		{`1e131071 * 10`, "22003", `value overflows numeric format`},
	} {
		t.Run(failure.code, func(t *testing.T) {
			jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(`CREATE TABLE jpa_source(id int PRIMARY KEY,doc jsonb,path jsonpath)`, 0))
				run(fixture.Exec(`INSERT INTO jpa_source VALUES(1,'2','$ + 1'),(2,'3',$1)`, 2, failure.path))
				run(fixture.Exec(`CREATE TABLE jpa_out(id int PRIMARY KEY,v jsonb,copy jsonb GENERATED ALWAYS AS (jsonb_path_query_array(v,'$')) STORED,CHECK(v <> '[]'::jsonb))`, 0))
				run(fixture.Exec(`CREATE INDEX jpa_out_idx ON jpa_out(jsonb_path_query_array(v,'$'))`, 0))
				run(fixture.Exec(`INSERT INTO jpa_out(id,v) VALUES(0,'99')`, 1))
				unchanged := func() {
					run(fixture.Select(`SELECT id,v,copy FROM jpa_out ORDER BY id`, []string{"id", "v", "copy"}, []string{"INT4", "JSONB", "JSONB"}, [][]any{{int64(0), `99`, `[99]`}}))
					run(fixture.Select(`SELECT id,doc FROM jpa_source ORDER BY id`, []string{"id", "doc"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `2`}, {int64(2), `3`}}))
				}
				for _, q := range []string{
					`INSERT INTO jpa_out(id,v) SELECT id,jsonb_path_query_array(doc,path) FROM jpa_source ORDER BY id`,
					`INSERT INTO jpa_out(id,v) VALUES(1,jsonb_path_query_array('2','$ + 1')),(2,jsonb_path_query_array('3',$1))`,
					`INSERT INTO jpa_out(id,v) SELECT id,jsonb_path_query_array(doc,path) FROM jpa_source ORDER BY id RETURNING id,v`,
					`UPDATE jpa_source SET doc=jsonb_path_query_array(doc,path) RETURNING id,doc`,
				} {
					args := []any(nil)
					if q == `INSERT INTO jpa_out(id,v) VALUES(1,jsonb_path_query_array('2','$ + 1')),(2,jsonb_path_query_array('3',$1))` {
						args = []any{failure.path}
					}
					s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic(failure.code, failure.message), args...)
					s.SelectRows = strings.Contains(q, " RETURNING ")
					for _, prepared := range []bool{false, true} {
						step := s
						if prepared {
							step = fixture.Prepared(step)
						}
						run(step)
						unchanged()
					}
				}
				q := `UPDATE jpa_source SET doc=jsonb_path_query_array(doc,path,'{}',$1)`
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic(failure.code, failure.message), false))
				unchanged()
				run(fixture.Exec(`UPDATE jpa_source SET path='$ + 1' WHERE id=2`, 1))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2, false))
				run(fixture.Select(`SELECT id,doc FROM jpa_source ORDER BY id`, []string{"id", "doc"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `[3]`}, {int64(2), `[4]`}}))
				run(fixture.Exec(`INSERT INTO jpa_out(id,v) VALUES(1,jsonb_path_query_array('[1,"x",3]','+$','{}',true))`, 1))
				run(fixture.Select(`SELECT v,copy FROM jpa_out WHERE id=1`, []string{"v", "copy"}, []string{"JSONB", "JSONB"}, [][]any{{`[1]`, `[[1]]`}}))
				// Arithmetic suppression cannot absorb errors from SQL children,
				// domain constraints, or the destination's CHECK constraint.
				run(fixture.ErrorDiagnostic(`INSERT INTO jpa_out(id,v) SELECT 3,jsonb_path_query_array(doc,'$ + 1','{}',id::text::boolean) FROM jpa_source WHERE id=2`, fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "2"`)))
				run(fixture.Exec(`CREATE DOMAIN jpa_doc AS jsonb CONSTRAINT jpa_positive CHECK(VALUE <> '0'::jsonb)`, 0))
				domain := fixture.ControlsReferenceDiagnostic("23514", `value for domain jpa_doc violates check constraint "jpa_positive"`)
				domain.Schema, domain.DataType, domain.Constraint = "public", "jpa_doc", "jpa_positive"
				run(fixture.ErrorDiagnostic(`INSERT INTO jpa_out(id,v) VALUES(3,jsonb_path_query_array('0'::jpa_doc,'$ + 1','{}',true))`, domain))
				check := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "jpa_out" violates check constraint "jpa_out_v_check"`)
				check.Detail = `Failing row contains (3, [], [[]]).`
				check.Schema, check.Table, check.Constraint = "public", "jpa_out", "jpa_out_v_check"
				run(fixture.ErrorDiagnostic(`INSERT INTO jpa_out(id,v) VALUES(3,jsonb_path_query_array('1','1 / 0','{}',true))`, check))
				run(fixture.Select(`SELECT id FROM jpa_out ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}}))
			})
		})
	}
}
