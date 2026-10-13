package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonCreateStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	for _, q := range []string{
		`CREATE DOMAIN jc_store_num AS numeric(8,3)`,
		`CREATE TYPE jc_store_pair AS (n int,t text)`,
		`CREATE TABLE jc_store(id int,n jc_store_num,a int[],r jc_store_pair,j json,b jsonb,d json DEFAULT to_json('x'::text),e jsonb DEFAULT jsonb_build_object('n',12.340::numeric),CHECK(json_build_array(id)::text <> '[0]'))`,
		`CREATE VIEW jc_store_view AS SELECT id,to_json(r) AS tj,to_jsonb(r) AS tb,array_to_json(a) AS aj,row_to_json(r) AS rj,json_build_array(n,'x'::text,NULL::int) AS ja,jsonb_build_array(VARIADIC a) AS ba,json_build_object('a',j,'a',b) AS jo,jsonb_build_object('a',j,'a',b) AS bo FROM jc_store`,
		`CREATE VIEW jc_literal_view AS SELECT to_json('x'::text) AS t,to_json(12.340::numeric) AS n,to_json(NULL::int) AS z,json_build_array(VARIADIC ARRAY[1,2]) AS a`,
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec(`INSERT INTO jc_store(id,n,a,r,j,b) VALUES(1,12.340,ARRAY[1,NULL],ROW(2,'y'),'{ "a":1,"a":2 }','{"a":3}')`, 1))
	check := fixture.Select(`SELECT id,tj,tb,aj,rj,ja,ba,jo,bo FROM jc_store_view ORDER BY id`, []string{"id", "tj", "tb", "aj", "rj", "ja", "ba", "jo", "bo"}, []string{"INT4", "JSON", "JSONB", "JSON", "JSON", "JSON", "JSONB", "JSON", "JSONB"}, [][]any{{int64(1), `{"n":2,"t":"y"}`, `{"n": 2, "t": "y"}`, `[1,null]`, `{"n":2,"t":"y"}`, `[12.340, "x", null]`, `[1, null]`, `{"a" : { "a":1,"a":2 }, "a" : {"a": 3}}`, `{"a": {"a": 3}}`}})
	jsonObjectBoth(t, p, b, c, check)
	jsonObjectBoth(t, p, b, c, fixture.Select(`SELECT d,e FROM jc_store`, []string{"d", "e"}, []string{"JSON", "JSONB"}, [][]any{{`"x"`, `{"n": 12.340}`}}))
	jsonObjectBoth(t, p, b, c, fixture.Select(`SELECT t,n,z,a FROM jc_literal_view`, []string{"t", "n", "z", "a"}, []string{"JSON", "JSON", "JSON", "JSON"}, [][]any{{`"x"`, `12.340`, nil, `[1, 2]`}}))
	return check
}

func TestReferenceJSONCreateConsumers(t *testing.T) {
	t.Run("authority", jsonCreateAuthority)
	jsonCreateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonCreateStored(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE VIEW jc_deparse_view AS SELECT to_json('x'::text) AS v`, 0))
		var definition string
		if err := c.QueryRowContext(p.Ctx, `SELECT pg_get_viewdef('jc_deparse_view'::regclass,false)`).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW jc_replayed_view AS "+definition, 0))
		jsonObjectBoth(t, p, b, c, jsonCreateSelect(`SELECT v FROM jc_replayed_view`, "JSON", `"x"`))
		jsonObjectBoth(t, p, b, c, jsonCreateSelect(`SELECT to_json('jc_store'::regclass) AS v`, "JSON", `"jc_store"`))
		jsonObjectBoth(t, p, b, c, fixture.Select(`SELECT CASE WHEN id=1 THEN to_json(r) ELSE row_to_json(r) END AS v FROM jc_store`, []string{"v"}, []string{"JSON"}, [][]any{{`{"n":2,"t":"y"}`}}))
		jsonObjectBoth(t, p, b, c, fixture.Select(`SELECT min(json_build_array(n)::text) AS v FROM jc_store`, []string{"v"}, []string{"TEXT"}, [][]any{{`[12.340]`}}))
		jsonObjectBoth(t, p, b, c, fixture.Select(`SELECT jsonb_build_array(n)::text AS v,count(*) AS n FROM jc_store GROUP BY jsonb_build_array(n)::text`, []string{"v", "n"}, []string{"TEXT", "INT8"}, [][]any{{`[12.340]`, int64(1)}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`UPDATE jc_store SET n=1.230 WHERE id=1 RETURNING to_json(n) AS v`, []string{"v"}, []string{"JSON"}, [][]any{{`1.230`}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`UPDATE jc_store SET n=12.340 WHERE id=1`, 1))
		for _, q := range []string{
			`INSERT INTO jc_store(id,j) VALUES(2,json_build_object('ok',2)),(3,json_build_object(NULL,3))`,
			`UPDATE jc_store SET j=json_build_object(NULL,id)`,
		} {
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22004", "null value not allowed for object key"))
			s.ErrorStage = "execute"
			jsonObjectBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
		}
		for _, expr := range []string{`to_json(id)`, `to_jsonb(id)`, `array_to_json(a)`, `row_to_json(r)`, `json_build_array()`, `jsonb_build_array()`, `json_build_object()`, `jsonb_build_object()`} {
			t.Run("stable "+expr, func(t *testing.T) {
				for _, tc := range []struct{ q, msg string }{
					{"CREATE INDEX jc_bad_idx ON jc_store ((" + expr + "::text))", "functions in index expression must be marked IMMUTABLE"},
					{"CREATE TABLE jc_bad_generated(id int,a int[],r jc_store_pair,bad text GENERATED ALWAYS AS (" + expr + "::text) STORED)", "generation expression is not immutable"},
				} {
					s := fixture.ErrorDiagnostic(tc.q, fixture.ControlsReferenceDiagnostic("42P17", tc.msg))
					s.ErrorStage = "execute"
					jsonObjectBoth(t, p, b, c, s)
				}
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`ALTER TABLE jc_store RENAME COLUMN r TO pair`, 0))
		jsonObjectBoth(t, p, b, c, check)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`ALTER TYPE jc_store_pair RENAME ATTRIBUTE t TO label`, 0))
		check.Rows = [][]any{{int64(1), `{"n":2,"label":"y"}`, `{"n": 2, "label": "y"}`, `[1,null]`, `{"n":2,"label":"y"}`, `[12.340, "x", null]`, `[1, null]`, `{"a" : { "a":1,"a":2 }, "a" : {"a": 3}}`, `{"a": {"a": 3}}`}}
		jsonObjectBoth(t, p, b, c, check)
	})
}
