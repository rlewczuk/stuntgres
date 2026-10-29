package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathNumericMethodsSequenceConsumers(t *testing.T) {
	for _, method := range []string{"abs", "floor", "ceiling", "number", "double"} {
		jsonpathNavigateCases(t, []jsonpathNavigateCase{
			{`[]`, `$.` + method + `()`, ``, `[]`},
			{`[1,2]`, `$.` + method + `()`, ``, `[1, 2]`},
			{`[1,2]`, `strict $[*].` + method + `()`, ``, `[1, 2]`},
			{`[1,1,2]`, `$[*].` + method + `()`, ``, `[1, 1, 2]`},
			{`null`, `exists($.` + method + `())`, ``, `[null]`},
			{`null`, `($.` + method + `()==1) is unknown`, ``, `[true]`},
			{`[null,1,2,"bad"]`, `$[*]?(@.` + method + `()==1)`, ``, `[1]`},
		})
	}
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, method := range []string{"abs", "floor", "ceiling", "number", "double"} {
			path := `$[*].` + method + `()`
			q := `SELECT jsonb_path_query_array($1,$2,'{}',$3) AS changed`
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, "22036", numericMethodsBad(method), `[1,"bad",2]`, path, false))
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(q, `[1]`, `[1,"bad",2]`, path, true))
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, "22036", numericMethodsKind(method), `[[]]`, `$.`+method+`()`, false))
			for _, f := range jsonpathQueryFunctions {
				call := `SELECT ` + f.name + `($1,$2,'{}',$3) AS changed`
				fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(call, "22036", numericMethodsBad(method), `[1,"bad"]`, `strict `+path, false))
			}
		}
		for _, s := range []fixture.Step{
			fixture.Select(`SELECT value,n FROM jsonb_path_query('[1.5,-1.5]','$[*].number()') WITH ORDINALITY AS q(value,n) ORDER BY n`, []string{"value", "n"}, []string{"JSONB", "INT8"}, [][]any{{`1.5`, int64(1)}, {`-1.5`, int64(2)}}),
			jsonpathQueryOne(`SELECT jsonb_path_query_first('[1.5,2]','$[*].number()') AS changed`, "JSONB", `1.5`),
			jsonpathQueryOne(`SELECT jsonb_path_exists('[1,"bad"]','$[*].number()') AS changed`, "BOOL", true),
			jsonpathNavigateError(`SELECT jsonb_path_query_first('[1,"bad"]','$[*].number()')`, "22036", numericMethodsInvalid("number", "bad")),
			jsonpathNavigateError(`SELECT jsonb_path_match('1','$.number()')`, "22038", `single boolean result is expected`),
			jsonpathQueryOne(`SELECT jsonb_path_query_first('{}','$.absent.number()') AS changed`, "JSONB", nil),
			jsonpathQueryOne(`SELECT jsonb_path_exists('{}','$.absent.number()') AS changed`, "BOOL", false),
			jsonpathNavigateError(`SELECT jsonb_path_match('{}','$.absent.number()')`, "22038", `single boolean result is expected`),
			jsonpathQueryOne(`SELECT jsonb_path_match('{}','$.absent.number()','{}',true) AS changed`, "BOOL", nil),
			jsonpathNavigateError(`SELECT jsonb_path_match('[1,2]','$[*].number()')`, "22038", `single boolean result is expected`),
			jsonpathQueryOne(`SELECT jsonb_path_query_first('[1,"bad"]','strict $[*].number()','{}',true) AS changed`, "JSONB", `1`),
			jsonpathQueryOne(`SELECT jsonb_path_exists('[1,"bad"]','strict $[*].number()','{}',true) AS changed`, "BOOL", nil),
			jsonpathNavigateSelect(`SELECT jsonb_path_query_array('null','($.abs()==1) is unknown') AS changed`, `[true]`),
			jsonpathQueryOne(`SELECT jsonb_path_match('1','$.number()','{}',true) AS changed`, "BOOL", nil),
			jsonpathQueryOne(`SELECT jsonb_path_match('1','$.number()==1') AS changed`, "BOOL", true),
			jsonpathQueryOne(`SELECT '1'::jsonb @? '$.number()'::jsonpath AS changed`, "BOOL", true),
			jsonpathQueryOne(`SELECT '1'::jsonb @@ '$.number()==1'::jsonpath AS changed`, "BOOL", true),
		} {
			fixture.JSONPathNavigateBoth(t, p, b, c, s)
		}
	})
}

func TestReferenceJSONPathNumericMethodsInputsMetadata(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := `SELECT jsonb_path_query_array($1,$2,$3,$4) AS changed`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, s := range []fixture.Step{
			jsonpathNavigateSelect(q, `[1.5, -1.5]`, `[1.5,-1.5]`, `$[*].number()`, `{}`, false),
			jsonpathNavigateError(q, "22036", numericMethodsInvalid("number", "bad"), `[1,"bad",2]`, `$[*].number()`, `{}`, false),
			jsonpathNavigateSelect(q, `[1]`, `[1,"bad",2]`, `$[*].number()`, `{}`, true),
			jsonpathNavigateSelect(q, `[1.235]`, `[1.235]`, `$[*].double()`, `{}`, false),
			jsonpathNavigateSelect(q, `[1499]`, `[1499]`, `$[*].ceiling()`, `{}`, false),
			jsonpathNavigateSelect(q, `[9223372036854775807]`, `[9223372036854775807]`, `$[*].number()`, `{}`, false),
			jsonpathNavigateSelect(q, `[42]`, `0`, `$v.number()`, `{"v":"42"}`, false),
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.JSONBSetMetadataStep(s, b.PG))
		}
		for _, args := range [][]any{
			{[]byte(`[1.25]`), []byte(`$[*].number()`), []byte(`{}`), false},
			{`[1.25]`, `$[*].number()`, `{}`, false},
		} {
			fixture.JSONPathNavigateBoth(t, p, b, c, fixture.JSONBSetMetadataStep(jsonpathNavigateSelect(q, `[1.25]`, args...), b.PG))
		}
		for slot := range 4 {
			args := []any{`1`, `$.number()`, `{}`, false}
			args[slot] = nil
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathQueryOne(q, "JSONB", nil, args...))
		}
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(`SELECT jsonb_path_query_array(path=>'$[*].number()',target=>'[1.25]') AS changed`, `[1.25]`))
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, "42704", `could not find jsonpath variable "missing"`, `1`, `$missing.number()`, `{}`, true))
		varsDiagnostic := fixture.ControlsReferenceDiagnostic("22023", `"vars" argument is not an object`)
		varsDiagnostic.Detail = `Jsonpath parameters should be encoded as key-value pairs of "vars" object.`
		varsError := fixture.ErrorDiagnostic(q, varsDiagnostic, `1`, `$.double()`, `[]`, true)
		varsError.SelectRows = true
		varsError.ErrorStage = "execute"
		fixture.JSONPathNavigateBoth(t, p, b, c, varsError)
		for _, tc := range []struct{ input, output string }{
			{` lax $ [*] . double ( ) `, `$[*].double()`},
			{`$.double()`, `$.double()`}, {`$.number()`, `$.number()`},
			{`strict $.number()`, `strict $.number()`},
		} {
			jsonpathReferenceOutput(t, p, b, tc.input, tc.output, false)
			jsonpathReferenceOutput(t, p, b, tc.input, tc.output, true)
		}
		for _, tc := range []struct{ q, message string }{
			{`SELECT jsonb_path_query_array(1,'$.number()')`, `function jsonb_path_query_array(integer, unknown) does not exist`},
			{`SELECT jsonb_path_query_array('1','$.double()','{}',false,true)`, `function jsonb_path_query_array(unknown, unknown, unknown, boolean, boolean) does not exist`},
		} {
			d := fixture.ControlsReferenceDiagnostic("42883", tc.message)
			d.Hint = `No function matches the given name and argument types. You might need to add explicit type casts.`
			d.Position = fixture.Position(8)
			s := fixture.ErrorDiagnostic(tc.q, d)
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE DOMAIN jpnm_doc AS jsonb CONSTRAINT jpnm_array CHECK(jsonb_typeof(VALUE)='array')`, 0))
		domain := fixture.ControlsReferenceDiagnostic("23514", `value for domain jpnm_doc violates check constraint "jpnm_array"`)
		domain.Schema, domain.DataType, domain.Constraint = "public", "jpnm_doc", "jpnm_array"
		domainError := fixture.ErrorDiagnostic(`SELECT jsonb_path_query_array('1'::jsonb::jpnm_doc,'$.number()',silent=>true)`, domain)
		domainError.SelectRows = true
		fixture.JSONPathNavigateBoth(t, p, b, c, domainError)
		rows, err := stmt.QueryContext(p.Ctx, `[1.235]`, `$[*].double()`, `{}`, false)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		if !b.PG {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathNavigateSelect(q, `[1.5]`, `1.5`, `$.number()`, `{}`, false))
		}
		fixture.StringCoreRetainedRows(t, rows, nil, []string{"changed"}, []string{"JSONB"}, [][]any{{`[1.235]`}}, false)
	})
}
