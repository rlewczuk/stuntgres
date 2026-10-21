package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathNavigateSuppression(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT jsonb_path_query_array($1::jsonb,$2::jsonpath,$3::jsonb,$4::boolean) AS changed"
		doc := `[{"x":1},{},{"x":3}]`
		for _, tc := range []struct {
			doc, path string
			silent    bool
			want      string
		}{
			{doc, `strict $[*].x`, true, `[1]`}, {doc, `$[*].x`, false, `[1, 3]`},
			{`[{}, {"x":3}]`, `strict $[*].x`, true, `[]`},
			{doc, `strict $[*]?(@.x > 0).x`, false, `[1, 3]`}, {doc, `strict $[*]?(@.x > 0).x`, true, `[1, 3]`},
		} {
			jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(q, tc.want, tc.doc, tc.path, `{}`, tc.silent))
		}
		jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, "2203A", `JSON object does not contain key "x"`, doc, `strict $[*].x`, `{}`, false))
		for _, silent := range []bool{false, true} {
			for _, vars := range []string{`[]`, `null`, `1`, `"x"`} {
				d := fixture.ControlsReferenceDiagnostic("22023", `"vars" argument is not an object`)
				d.Detail = `Jsonpath parameters should be encoded as key-value pairs of "vars" object.`
				s := fixture.ErrorDiagnostic(q, d, `1`, `$`, vars, silent)
				s.SelectRows = true
				s.ErrorStage = "execute"
				jsonpathNavigateBoth(t, p, b, c, s)
			}
			for _, path := range []string{`$v`, `exists($v)`, `$v == 1`, `("x" == 1) && ($v == 1)`, `("x" == 1) || ($v == 1)`} {
				jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, "42704", `could not find jsonpath variable "v"`, `1`, path, `{}`, silent))
			}
		}
	})
}

func TestReferenceJSONPathNavigateCapabilityBoundary(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT jsonb_path_query_array($1::jsonb,$2::jsonpath,'{}'::jsonb,$3::boolean) AS changed"
		for _, tc := range []struct{ path, want, message string }{
			{`1+2`, `[3]`, `stuntgres: JSONPath arithmetic is unsupported`},
			{`$.double()`, `[1, 2]`, `stuntgres: JSONPath scalar methods are unsupported`},
			{`exists($.double())`, `[true]`, `stuntgres: JSONPath scalar methods are unsupported`},
			{`$.double() > 0`, `[true]`, `stuntgres: JSONPath scalar methods are unsupported`},
			{`exists(1+2)`, `[true]`, `stuntgres: JSONPath arithmetic is unsupported`},
			{`(1+2) == 3`, `[true]`, `stuntgres: JSONPath arithmetic is unsupported`},
		} {
			for _, silent := range []bool{false, true} {
				s := jsonpathNavigateSelect(q, tc.want, `[1,2]`, tc.path, silent)
				if !b.PG {
					s = jsonpathNavigateError(q, "0A000", tc.message, `[1,2]`, tc.path, silent)
				}
				jsonpathNavigateBoth(t, p, b, c, s)
			}
		}
		for _, tc := range []struct{ path, want string }{{`$.size()`, `[2]`}, {`exists($.size())`, `[true]`}, {`$.size()>0`, `[true]`}} {
			for _, silent := range []bool{false, true} {
				jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(q, tc.want, `[1,2]`, tc.path, silent))
			}
		}
		for _, tc := range []struct{ path, want string }{{`$.missing.size()`, `[]`}, {`(1==1)||($.size()>0)`, `[true]`}, {`$.missing.double()`, `[]`}, {`(1==1)||($.double()>0)`, `[true]`}} {
			jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(q, tc.want, `{}`, tc.path, true))
		}
		jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(q, nil, nil, `$.size()`, true))
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		s := jsonpathNavigateSelect(q, `[3]`, `1`, `1+2`, true)
		if !b.PG {
			s = jsonpathNavigateError(q, "0A000", `stuntgres: JSONPath arithmetic is unsupported`, `1`, `1+2`, true)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathNavigateSelect(q, `[1, 2]`, `[1,2]`, `$[*]`, false))
	})
}
