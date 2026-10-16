package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathNavigateAtomicity(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE jpn_source(id int PRIMARY KEY,doc jsonb)`, 0))
		run(fixture.Exec(`INSERT INTO jpn_source VALUES(1,'{"x":1}'),(2,'{}')`, 2))
		run(fixture.Exec(`CREATE TABLE jpn_out(id int PRIMARY KEY,v jsonb,copy jsonb GENERATED ALWAYS AS (jsonb_path_query_array(v,'$')) STORED,CHECK(v <> '[]'::jsonb))`, 0))
		run(fixture.Exec(`CREATE INDEX jpn_out_idx ON jpn_out(jsonb_path_query_array(v,'$'))`, 0))
		run(fixture.Exec(`INSERT INTO jpn_out(id,v) VALUES(0,'99')`, 1))
		unchanged := func() {
			run(fixture.Select(`SELECT id,v,copy FROM jpn_out ORDER BY id`, []string{"id", "v", "copy"}, []string{"INT4", "JSONB", "JSONB"}, [][]any{{int64(0), `99`, `[99]`}}))
		}
		for _, q := range []string{
			`INSERT INTO jpn_out(id,v) SELECT id,jsonb_path_query_array(doc,'strict $.x') FROM jpn_source ORDER BY id`,
			`INSERT INTO jpn_out(id,v) VALUES(1,jsonb_path_query_array('{"x":1}','strict $.x')),(2,jsonb_path_query_array('{}','strict $.x'))`,
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("2203A", `JSON object does not contain key "x"`))
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
				unchanged()
			}
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(`INSERT INTO jpn_out(id,v) SELECT id,jsonb_path_query_array(doc,'strict $.x') FROM jpn_source ORDER BY id RETURNING id,v`, fixture.ControlsReferenceDiagnostic("2203A", `JSON object does not contain key "x"`))
			s.SelectRows = true
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
			unchanged()
		}
		run(fixture.Exec(`CREATE TABLE jpn_update(id int PRIMARY KEY,doc jsonb,v jsonb)`, 0))
		run(fixture.Exec(`INSERT INTO jpn_update VALUES(1,'{"x":1}','99'),(2,'{}','99')`, 2))
		q := `UPDATE jpn_update SET v=jsonb_path_query_array(doc,'strict $.x','{}',$1)`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("2203A", `JSON object does not contain key "x"`), false))
		run(fixture.Select(`SELECT id,v FROM jpn_update ORDER BY id`, []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `99`}, {int64(2), `99`}}))
		run(fixture.Exec(`UPDATE jpn_update SET doc='{"x":2}' WHERE id=2`, 1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2, false))
		run(fixture.Select(`SELECT id,v FROM jpn_update ORDER BY id`, []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `[1]`}, {int64(2), `[2]`}}))
		run(fixture.Exec(`INSERT INTO jpn_out(id,v) SELECT id,jsonb_path_query_array(doc,'strict $.x','{}',true) FROM jpn_update ORDER BY id`, 2))
		run(fixture.Select(`SELECT id,v,copy FROM jpn_out ORDER BY id`, []string{"id", "v", "copy"}, []string{"INT4", "JSONB", "JSONB"}, [][]any{{int64(0), `99`, `[99]`}, {int64(1), `[1]`, `[[1]]`}, {int64(2), `[2]`, `[[2]]`}}))
		// Silent only covers evaluator structural errors, never SQL child conversion.
		run(fixture.ErrorDiagnostic(`INSERT INTO jpn_out(id,v) SELECT 3,jsonb_path_query_array(doc,'$','{}',id::text::boolean) FROM jpn_source WHERE id=2`, fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "2"`)))
		run(fixture.Select(`SELECT count(*) AS n FROM jpn_out`, []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
		run(fixture.Exec(`CREATE TABLE jpn_prefix(id int,v jsonb)`, 0))
		run(fixture.Exec(`INSERT INTO jpn_prefix VALUES(1,jsonb_path_query_array('[{"x":1},{},{"x":3}]','strict $[*].x','{}',true)),(2,jsonb_path_query_array('[{},{"x":3}]','strict $[*].x','{}',true))`, 2))
		run(fixture.Select(`SELECT id,v FROM jpn_prefix ORDER BY id`, []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `[1]`}, {int64(2), `[]`}}))
	})
}
