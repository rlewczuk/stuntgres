package reference_test

import (
	"database/sql"
	"sync"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathMethodsConsumers(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			jsonpathNavigateSelect(`SELECT jsonb_path_query_array('[0,1]','$[*].boolean()') AS changed`, `[false, true]`),
			fixture.Select(`SELECT value,n FROM jsonb_path_query('[0,1]','$[*].boolean()') WITH ORDINALITY AS q(value,n) ORDER BY n`, []string{"value", "n"}, []string{"JSONB", "INT8"}, [][]any{{`false`, int64(1)}, {`true`, int64(2)}}),
			jsonpathQueryOne(`SELECT jsonb_path_query_first('[0,1]','$[*].boolean()') AS changed`, "JSONB", `false`),
			jsonpathQueryOne(`SELECT jsonb_path_exists('0','$.boolean()') AS changed`, "BOOL", true),
			jsonpathQueryOne(`SELECT jsonb_path_match('0','$.boolean()') AS changed`, "BOOL", false),
			jsonpathQueryOne(`SELECT '0'::jsonb @? '$.boolean()'::jsonpath AS changed`, "BOOL", true),
			jsonpathQueryOne(`SELECT '0'::jsonb @@ '$.boolean()'::jsonpath AS changed`, "BOOL", false),
			jsonpathNavigateSelect(`SELECT jsonb_path_query_array(path=>'$[*].type()',target=>'[null,1]') AS changed`, `["null", "number"]`),
			jsonpathNavigateSelect(`SELECT jsonb_path_query_array($1,$2) AS changed`, `["number"]`, `1`, `$.type()`),
		} {
			fixture.JSONPathNavigateBoth(t, p, b, c, s)
		}
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT jsonb_path_match('[0,1]','$[*].boolean()')`, "22038", `single boolean result is expected`))
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT jsonb_path_match('[0,1]','$[*].boolean()','{}',true) AS changed`, "BOOL", nil))
		for _, f := range jsonpathQueryFunctions {
			for slot := range 4 {
				args := []any{`1`, `$.type()`, `{}`, false}
				args[slot] = nil
				rows := jsonpathQueryRows(nil)
				if f.name == "jsonb_path_query" {
					rows = nil
				}
				fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathQueryResult(`SELECT `+f.name+`($1,$2,$3,$4) AS changed`, f.typ, rows, args...))
			}
			q := `SELECT ` + f.name + `($1,$2,'{}',$3) AS changed`
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, "22036", `argument "bad" of jsonpath item method .boolean() is invalid for type boolean`, `[0,1,"bad"]`, `strict $[*].boolean()`, false))
		}
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT jsonb_path_exists('[0,"bad"]','$[*].boolean()') AS changed`, "BOOL", true))
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT jsonb_path_query_first('[0,"bad"]','$[*].boolean()')`, "22036", `argument "bad" of jsonpath item method .boolean() is invalid for type boolean`))
		for _, tc := range []struct {
			name, typ string
			want      any
		}{{"jsonb_path_query_array", "JSONB", `[false]`}, {"jsonb_path_query_first", "JSONB", `false`}, {"jsonb_path_match", "BOOL", false}, {"jsonb_path_exists", "BOOL", nil}} {
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT `+tc.name+`('[0,"bad"]','strict $[*].boolean()','{}',true) AS changed`, tc.typ, tc.want))
		}
	})
}

func TestReferenceJSONPathMethodsHeld(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := `SELECT jsonb_path_query_array($1,$2,$3,$4) AS changed`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "1" of jsonpath input`)
		d.Context = "unnamed portal parameter $2 = '...'"
		badPath := fixture.ErrorDiagnostic(q, d, `1`, `$.boolean(1)`, `{}`, false)
		badPath.SelectRows = true
		badPath.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, badPath)
		for _, s := range []fixture.Step{
			jsonpathNavigateSelect(q, `["array"]`, jsonpathMethodsA, `$.type()`, `{}`, false),
			jsonpathNavigateError(q, "22036", `argument "bad" of jsonpath item method .boolean() is invalid for type boolean`, `[1,"bad"]`, `$[*].boolean()`, `{}`, false),
			jsonpathNavigateSelect(q, `[true]`, `[1,"bad"]`, `$[*].boolean()`, `{}`, true),
			jsonpathNavigateSelect(q, `["1.2300", "xyz", "false"]`, jsonpathMethodsC, `$[*].string()`, `{}`, false),
			jsonpathNavigateSelect(q, `[true]`, `0`, `$v.boolean()`, `{"v":"yes"}`, false),
			jsonpathNavigateError(q, "22039", jsonpathMethodsSizeError, `1`, `strict $.size()`, `{}`, false),
			jsonpathNavigateSelect(q, `["x", "y", "z"]`, jsonpathMethodsO, `$.keyvalue().key`, `{}`, false),
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		shared, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer shared.Close()
		var workers sync.WaitGroup
		for range 4 {
			workers.Go(func() {
				for range 5 {
					jsonpathMethodsPairs(t, p, fixture.HeldStatement{Executor: b.DB, Statement: shared, SQL: q}, q, []any{`[{"a":1},{"a":1}]`, `$[0,0,1].keyvalue()`, `{}`, false}, []string{"a", "a", "a"}, []string{`1`, `1`, `1`}, []int{0, 0, 1})
				}
			})
		}
		workers.Wait()
		// Retain the actual cursor and its descriptors over another invocation.
		rows, err := stmt.QueryContext(p.Ctx, jsonpathMethodsC, `$[*].string()`, `{}`, false)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		meta, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if !b.PG {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathNavigateSelect(q, `[1]`, `7`, `$.size()`, `{}`, false))
		}
		fixture.StringCoreRetainedRows(t, rows, nil, []string{"changed"}, []string{"JSONB"}, [][]any{{`["1.2300", "xyz", "false"]`}}, false)
		if len(meta) != 1 || meta[0].DatabaseTypeName() != "JSONB" {
			t.Fatal(meta)
		}
	})
}
