package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONCreateParameters(t *testing.T) {
	jsonCreateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TYPE jc_param_pair AS (n int,t text)`, 0))
		for _, tc := range []struct {
			q, typ string
			want   any
			args   []any
		}{
			{`SELECT to_json($1::text) AS v`, "JSON", `"x"`, []any{"x"}},
			{`SELECT to_jsonb($1::numeric) AS v`, "JSONB", `12.340`, []any{"12.340"}},
			{`SELECT array_to_json($1::int[],$2::bool) AS v`, "JSON", "[1,\n 2]", []any{"{1,2}", true}},
			{`SELECT row_to_json($1::jc_param_pair) AS v`, "JSON", `{"n":1,"t":"x"}`, []any{"(1,x)"}},
			{`SELECT json_build_array($1::text,$2::int) AS v`, "JSON", `["x", 2]`, []any{"x", 2}},
			{`SELECT jsonb_build_array($1::json,$2::jsonb) AS v`, "JSONB", `[{"a": 2}, [1, 2]]`, []any{`{"a":1,"a":2}`, `[1,2]`}},
			{`SELECT json_build_object($1::text,$2::int) AS v`, "JSON", `{"a" : 2}`, []any{"a", 2}},
			{`SELECT jsonb_build_object($1::text,$2::int) AS v`, "JSONB", `{"a": 2}`, []any{"a", 2}},
			{`SELECT json_build_array(VARIADIC $1::int[]) AS v`, "JSON", `[1, null, 2]`, []any{"{1,NULL,2}"}},
			{`SELECT jsonb_build_object(VARIADIC $1::text[]) AS v`, "JSONB", `{"a": "2"}`, []any{"{a,1,a,2}"}},
			{`SELECT jsonb_build_array(VARIADIC $1::int[]) AS v`, "JSONB", `[1, null, 2]`, []any{"{1,NULL,2}"}},
			{`SELECT json_build_object(VARIADIC $1::text[]) AS v`, "JSON", `{"a" : "1", "a" : "2"}`, []any{"{a,1,a,2}"}},
			{`SELECT array_to_json(ARRAY[1],$1) AS v`, "JSON", "[1]", []any{true}},
			{`SELECT row_to_json(ROW(1),$1) AS v`, "JSON", `{"f1":1}`, []any{false}},
			{`SELECT to_json($1::int) AS v`, "JSON", nil, []any{nil}},
			{`SELECT to_jsonb($1::json) AS v`, "JSONB", `null`, []any{"null"}},
		} {
			t.Run(tc.q, func(t *testing.T) {
				good := jsonCreateSelect(tc.q, tc.typ, tc.want, tc.args...)
				jsonObjectBoth(t, p, b, c, good)
				stmt, err := c.PrepareContext(p.Ctx, tc.q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
			})
		}
		for _, name := range []string{"to_json", "to_jsonb", "array_to_json", "row_to_json", "json_build_array", "jsonb_build_array", "json_build_object", "jsonb_build_object"} {
			d := fixture.ControlsReferenceDiagnostic("42P18", "could not determine data type of parameter $1")
			// Polymorphic calls fail before unresolved-parameter validation.
			if name == "to_json" || name == "to_jsonb" || name == "array_to_json" {
				d = fixture.ControlsReferenceDiagnostic("42804", "could not determine polymorphic type because input has type unknown")
			}
			if name == "row_to_json" {
				d = fixture.ControlsReferenceDiagnostic("0A000", "input of anonymous composite types is not implemented")
				d.Context = "unnamed portal parameter $1 = '...'"
			}
			t.Run(name+" unknown parameter", func(t *testing.T) {
				jsonCreateError(t, p, b, c, "SELECT "+name+"($1) AS v", d, name != "row_to_json", "x")
			})
		}
		for _, name := range []string{"json_build_array", "jsonb_build_array"} {
			q := "SELECT " + name + "($1) AS v,$1::int AS n"
			d := fixture.ControlsReferenceDiagnostic("42P08", "could not determine data type of parameter $1")
			d.Position = fixture.Position(len([]rune("SELECT "+name+"(")) + 1)
			t.Run(name+" earlier any", func(t *testing.T) { jsonCreateError(t, p, b, c, q, d, true, 2) })
			q = "SELECT $1::int AS n," + name + "($1) AS v"
			typ := "JSON"
			if name == "jsonb_build_array" {
				typ = "JSONB"
			}
			jsonObjectBoth(t, p, b, c, fixture.Select(q, []string{"n", "v"}, []string{"INT4", typ}, [][]any{{int64(2), `[2]`}}, 2))
		}
		t.Run("sparse parameter", func(t *testing.T) {
			jsonCreateError(t, p, b, c, `SELECT json_build_array($2::int) AS v`, fixture.ControlsReferenceDiagnostic("42P18", "could not determine data type of parameter $1"), true, nil, 2)
		})
		q := `SELECT json_build_array($1::text) AS v`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		owned := []byte("é\"\\\n")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonCreateSelect(q, "JSON", `["é\"\\\n"]`, owned))
		for i := range owned {
			owned[i] = 'x'
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonCreateSelect(q, "JSON", `[null]`, nil))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonCreateSelect(q, "JSON", `["é\"\\\n"]`, "é\"\\\n"))
		for _, name := range []string{"json_build_object", "jsonb_build_object"} {
			q := "SELECT " + name + "($1::text,$2::int) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			typ, want := "JSON", `{"a" : 1}`
			d := fixture.ControlsReferenceDiagnostic("22004", "null value not allowed for object key")
			if name == "jsonb_build_object" {
				typ, want = "JSONB", `{"a": 1}`
				d = fixture.ControlsReferenceDiagnostic("22023", "argument 1: key must not be null")
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonCreateSelect(q, typ, want, "a", 1))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonCreateSelect(q, typ, map[string]string{"JSON": `{"a" : null}`, "JSONB": `{"a": null}`}[typ], "a", nil))
			s := fixture.ErrorDiagnostic(q, d, nil, 1)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonCreateSelect(q, typ, want, "a", 1))
			stmt.Close()
		}
	})
}
