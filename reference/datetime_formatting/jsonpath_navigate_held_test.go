package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathNavigateMetadata(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			expr string
			want any
		}{
			{`jsonb_path_query_array('[1,2]','$[*]')`, `[1, 2]`},
			{`jsonb_path_query_array('{}','$.missing')`, `[]`},
			{`jsonb_path_query_array('null','$')`, `[null]`},
			{`jsonb_path_query_array('"foo"','$ > 0')`, `[null]`},
			{`jsonb_path_query_array(NULL::jsonb,'$')`, nil},
		} {
			for _, empty := range []bool{false, true} {
				q := "SELECT " + tc.expr + " AS changed"
				rows := [][]any{{tc.want}}
				if empty {
					q += " WHERE false"
					rows = nil
				}
				s := fixture.JSONBSetMetadataStep(fixture.Select(q, []string{"changed"}, []string{"JSONB"}, rows), b.PG)
				fixture.JSONPathNavigateBoth(t, p, b, c, s)
			}
		}
		s := fixture.Select(`SELECT jsonb_path_query_array('1','$')`, []string{"jsonb_path_query_array"}, []string{"JSONB"}, [][]any{{`[1]`}})
		s.CheckMetadata = func(t *testing.T, r *sql.Rows) { fixture.JSONBObjectMetadata(t, r, b.PG, "jsonb_path_query_array") }
		fixture.JSONPathNavigateBoth(t, p, b, c, s)
	})
}

func TestReferenceJSONPathNavigateHeld(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := `SELECT jsonb_path_query_array($1,$2,$3,$4) AS changed`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
		good := fixture.JSONBSetMetadataStep(jsonpathNavigateSelect(q, `[2, 3]`, `[1,2,3]`, `$[*]?(@ >= $n)`, `{"n":2}`, false), b.PG)
		run(good)
		run(jsonpathNavigateError(q, "2203A", `JSON object does not contain key "x"`, `[{"x":1},{}]`, `strict $[*].x`, `{}`, false))
		run(good)
		run(jsonpathNavigateSelect(q, `[1]`, `[{"x":1},{},{"x":3}]`, `strict $[*].x`, `{}`, true))
		run(jsonpathNavigateError(q, "42704", `could not find jsonpath variable "v"`, `1`, `$v`, `{}`, true))
		run(good)
		for range 3 {
			run(jsonpathNavigateSelect(q, `[2, 3]`, `[[1,2],[3]]`, `$[*][last]`, `{}`, false))
			run(jsonpathNavigateSelect(q, `["A.B"]`, `["A.B","axb"]`, `$[*]?(@ like_regex "a.b" flag "iq")`, `{}`, false))
			run(jsonpathNavigateSelect(q, `[1]`, `[{"x":1,"c":[{"y":2}]},{"x":2,"c":[{"y":0}]}]`, `$[*]?(exists(@.c[*]?(@.y > 1))).x`, `{}`, false))
		}
		if !b.PG {
			old, err := stmt.QueryContext(p.Ctx, good.Args...)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			meta, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			run(jsonpathNavigateSelect(q, `[]`, `{}`, `$.missing`, `{}`, false))
			if len(meta) != 1 || meta[0].DatabaseTypeName() != "JSONB" {
				t.Fatal(meta)
			}
			fixture.StringCoreRetainedRows(t, old, nil, good.Columns, good.Types, good.Rows, false)
		}
	})
}
