package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONCreateFeatureSmoke(t *testing.T) {
	jsonCreateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			expr, typ string
			want      any
		}{
			{`to_json(ROW(1,ARRAY[2,NULL]))`, "JSON", `{"f1":1,"f2":[2,null]}`},
			{`to_jsonb(ROW(1,ARRAY[2,NULL]))`, "JSONB", `{"f1": 1, "f2": [2, null]}`},
			{`array_to_json(ARRAY[[1,2],[3,4]],true)`, "JSON", "[[1,2],\n [3,4]]"},
			{`row_to_json(ROW(1,'x'))`, "JSON", `{"f1":1,"f2":"x"}`},
			{`json_build_array(1,'x',NULL)`, "JSON", `[1, "x", null]`},
			{`jsonb_build_array(1,'x',NULL)`, "JSONB", `[1, "x", null]`},
			{`json_build_object('a',1,'a',2)`, "JSON", `{"a" : 1, "a" : 2}`},
			{`jsonb_build_object('a',1,'a',2)`, "JSONB", `{"a": 2}`},
			{`json_build_array()`, "JSON", `[]`},
			{`to_json(NULL::text)`, "JSON", nil},
			{`json_build_array(VARIADIC ARRAY[[1,4],[2,5]])`, "JSON", `[1, 4, 2, 5]`},
		} {
			s := jsonCreateSelect("SELECT "+tc.expr+" AS v", tc.typ, tc.want)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { jsonCreateMetadata(t, r, b.PG, tc.typ) }
			jsonObjectBoth(t, p, b, c, s)
		}
		jsonObjectBoth(t, p, b, c, jsonCreateSelect(`SELECT to_json($1::text) AS v`, "JSON", `"é\"\\\n"`, "é\"\\\n"))
		for _, name := range []string{"json_build_object", "jsonb_build_object"} {
			d := fixture.ControlsReferenceDiagnostic("22023", "argument list must have even number of elements")
			d.Hint = "The arguments of " + name + "() must consist of alternating keys and values."
			jsonCreateError(t, p, b, c, "SELECT "+name+"('a') AS v", d, false)
			d = fixture.ControlsReferenceDiagnostic("22004", "null value not allowed for object key")
			if name == "jsonb_build_object" {
				d = fixture.ControlsReferenceDiagnostic("22023", "argument 1: key must not be null")
			}
			jsonCreateError(t, p, b, c, "SELECT "+name+"(NULL,1) AS v", d, false)
			q := "SELECT " + name + "($1::text,1) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			s := fixture.ErrorDiagnostic(q, d, nil)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			typ, want := "JSON", `{"a" : 1}`
			if name == "jsonb_build_object" {
				typ, want = "JSONB", `{"a": 1}`
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonCreateSelect(q, typ, want, "a"))
			stmt.Close()
		}
		jsonCreateEnvironment(t, p, b, c)
		check := jsonCreateStored(t, p, b, c)
		jsonCreateControls(t, p, b, c, check)
	})
}
