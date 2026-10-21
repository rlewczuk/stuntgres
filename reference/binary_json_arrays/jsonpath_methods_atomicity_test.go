package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathMethodsAtomicity(t *testing.T) {
	for _, tc := range []struct{ method, bad, good, code, message, want string }{
		{"boolean", `"bad"`, `1`, "22036", `argument "bad" of jsonpath item method .boolean() is invalid for type boolean`, `[true]`},
		{"keyvalue", `0`, `{"a":1}`, "2203C", jsonpathMethodsKeyValueError, `["a"]`},
	} {
		t.Run(tc.method, func(t *testing.T) {
			jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				path := `$.` + tc.method + `()`
				if tc.method == "keyvalue" {
					path += `.key`
				}
				run(fixture.Exec(`CREATE TABLE jpm_source(id int PRIMARY KEY,doc jsonb,path jsonpath)`, 0))
				run(fixture.Exec(`INSERT INTO jpm_source VALUES(1,$1,$3),(2,$2,$3)`, 2, tc.good, tc.bad, path))
				run(fixture.Exec(`CREATE TABLE jpm_out(id int PRIMARY KEY,v jsonb,copy jsonb GENERATED ALWAYS AS(jsonb_path_query_array(v,'$.type()')) STORED,CHECK(v<>'[]'::jsonb))`, 0))
				run(fixture.Exec(`CREATE INDEX jpm_out_expr ON jpm_out(jsonb_path_query_array(v,'$.type()'))`, 0))
				run(fixture.Exec(`INSERT INTO jpm_out(id,v) VALUES(0,'99')`, 1))
				unchanged := func() {
					run(fixture.Select(`SELECT id,v,copy FROM jpm_out ORDER BY id`, []string{"id", "v", "copy"}, []string{"INT4", "JSONB", "JSONB"}, [][]any{{int64(0), `99`, `["number"]`}}))
				}
				for i, q := range []string{
					`INSERT INTO jpm_out(id,v) SELECT id,jsonb_path_query_array(doc,path) FROM jpm_source ORDER BY id`,
					`INSERT INTO jpm_out(id,v) VALUES(1,jsonb_path_query_array($1,$3)),(2,jsonb_path_query_array($2,$3))`,
				} {
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic(tc.code, tc.message))
						if i == 1 {
							s.Args = []any{tc.good, tc.bad, path}
						}
						if prepared {
							s = fixture.Prepared(s)
						}
						run(s)
						unchanged()
					}
				}
				q := `UPDATE jpm_source SET doc=$1 WHERE id=2`
				run(fixture.Exec(q, 1, tc.good))
				run(fixture.Exec(`ALTER TABLE jpm_source ADD COLUMN result jsonb`, 0))
				run(fixture.Exec(`UPDATE jpm_source SET result='99'`, 2))
				run(fixture.Exec(q, 1, tc.bad))
				update := `UPDATE jpm_source SET result=jsonb_path_query_array(doc,path,'{}',$1)`
				stmt, err := c.PrepareContext(p.Ctx, update)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: update}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(update, fixture.ControlsReferenceDiagnostic(tc.code, tc.message), false))
				run(fixture.Select(`SELECT id,result FROM jpm_source ORDER BY id`, []string{"id", "result"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `99`}, {int64(2), `99`}}))
				run(fixture.Exec(q, 1, tc.good))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(update, 2, false))
				run(fixture.Select(`SELECT id,result FROM jpm_source ORDER BY id`, []string{"id", "result"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), tc.want}, {int64(2), tc.want}}))
				jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(`SELECT jsonb_path_query_array('[1,"bad",0]','$[*].boolean()','{}',true) AS changed`, `[true]`))
				child := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "bad"`)
				child.Position = fixture.Position(86)
				run(fixture.ErrorDiagnostic(`INSERT INTO jpm_out(id,v) VALUES(3,jsonb_path_query_array('[1]','$[*].string()','{}','bad'::boolean))`, child))
				unchanged()
			})
		})
	}
}
