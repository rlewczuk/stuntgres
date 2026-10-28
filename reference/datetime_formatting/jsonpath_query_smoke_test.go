package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathQueryFeatureSmoke(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := `SELECT jsonb_path_exists($1,$2,$3) AS present,jsonb_path_match($1,'exists($.a[*]?(@ >= $min && @ <= $max))',$3) AS predicate,jsonb_path_query_first($1,$2,$3) AS first,jsonb_path_query_array($1,$2,$3) AS collected`
		s := fixture.Select(q, []string{"present", "predicate", "first", "collected"}, []string{"BOOL", "BOOL", "JSONB", "JSONB"}, [][]any{{true, true, `2`, `[2, 3, 4]`}}, jsonpathQueryDocument, jsonpathQueryPath, jsonpathQueryVars)
		s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
			cts, err := r.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if len(cts) != 4 {
				t.Fatal(cts)
			}
			for i, typ := range s.Types {
				if cts[i].DatabaseTypeName() != typ {
					t.Fatal(cts)
				}
			}
		}
		fixture.JSONPathNavigateBoth(t, p, b, c, s)
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathQueryResult(`SELECT jsonb_path_query($1,$2,$3) AS changed`, "JSONB", jsonpathQueryRows(`2`, `3`, `4`), jsonpathQueryDocument, jsonpathQueryPath, jsonpathQueryVars))
		from := fixture.Select(`SELECT * FROM jsonb_path_query($1,$2,$3) WITH ORDINALITY AS q(v,n)`, []string{"v", "n"}, []string{"JSONB", "INT8"}, [][]any{{`2`, int64(1)}, {`3`, int64(2)}, {`4`, int64(3)}}, jsonpathQueryDocument, jsonpathQueryPath, jsonpathQueryVars)
		from.CheckMetadata = func(t *testing.T, r *sql.Rows) {
			cts, err := r.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if len(cts) != 2 || cts[0].DatabaseTypeName() != "JSONB" || cts[1].DatabaseTypeName() != "INT8" {
				t.Fatal(cts)
			}
		}
		fixture.JSONPathNavigateBoth(t, p, b, c, from)
		heldSQL := `SELECT jsonb_path_query($1,$2,$3,$4) AS changed`
		stmt, err := c.PrepareContext(p.Ctx, heldSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: heldSQL}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathNavigateError(heldSQL, "2203A", `JSON object does not contain key "x"`, `[{"x":1},{}]`, `strict $[*].x`, `{}`, false))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathQueryOne(heldSQL, "JSONB", `1`, `[{"x":1},{}]`, `strict $[*].x`, `{}`, true))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathQueryResult(heldSQL, "JSONB", jsonpathQueryRows(`2`, `3`, `4`), jsonpathQueryDocument, jsonpathQueryPath, jsonpathQueryVars, false))
		fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(`SELECT jsonb_path_exists('[{"x":true},{}]','strict $[*].x','{}',true) AS present,jsonb_path_match('[{"x":true},{}]','strict $[*].x','{}',true) AS predicate,jsonb_path_query_first('[{"x":1},{}]','strict $[*].x','{}',true) AS first`, []string{"present", "predicate", "first"}, []string{"BOOL", "BOOL", "JSONB"}, [][]any{{nil, true, `1`}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE VIEW jpq_smoke_scalar AS SELECT jsonb_path_query_first('{"a":[2,3,4]}','$.a[*]') AS first`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE VIEW jpq_smoke_rows AS SELECT jsonb_path_query('{"a":[2,3,4]}','$.a[*]') AS v`, 0))
		fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(`SELECT first,v FROM jpq_smoke_scalar CROSS JOIN jpq_smoke_rows ORDER BY v`, []string{"first", "v"}, []string{"JSONB", "JSONB"}, [][]any{{`2`, `2`}, {`2`, `3`}, {`2`, `4`}}))
	})
}
