package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func jsonArrayAggregateSmokeDiagnostics(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	q := "/*é😀*/ SELECT json_agg(1,2)"
	d := fixture.ControlsReferenceDiagnostic("42883", "function json_agg(integer, integer) does not exist")
	d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	d.Position = jsonArrayAggregatePosition(q, "json_agg")
	jsonArrayAggregateError(t, p, b, q, d, true)
	q = "/*é😀*/ SELECT json_arrayagg(1 RETURNING bytea FORMAT JSON ENCODING UTF16)"
	d = fixture.ControlsReferenceDiagnostic("0A000", "unsupported JSON encoding")
	d.Hint, d.Position = "Only UTF8 JSON encoding is supported.", jsonArrayAggregatePosition(q, "FORMAT")
	jsonArrayAggregateError(t, p, b, q, d, true)
}

// Quoted names do not alias builtin aggregates, but normal one-argument
// function lookup still permits an exact composite-field projection.
func TestReferenceJSONArrayAggregateQuotedFieldCollision(t *testing.T) {
	jsonArrayAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{
			`CREATE TYPE ja_quoted_projection AS ("JSON_AGG" integer,"Json_Agg" integer,"JSONB_AGG" integer,"Jsonb_Agg" integer,"JSON_AGG_STRICT" integer,"Json_Agg_Strict" integer,"JSONB_AGG_STRICT" integer,"Jsonb_Agg_Strict" integer)`,
			`CREATE TYPE ja_quoted_no_collision AS (other_field integer)`,
		} {
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec(q,0))
		}
		const row = "ROW(7,8,9,10,11,12,13,14)::ja_quoted_projection"
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct{name string;value int64}{
			{"JSON_AGG",7},{"Json_Agg",8},{"JSONB_AGG",9},{"Jsonb_Agg",10},
			{"JSON_AGG_STRICT",11},{"Json_Agg_Strict",12},{"JSONB_AGG_STRICT",13},{"Jsonb_Agg_Strict",14},
		} {
			t.Run(tc.name,func(t *testing.T){
				quoted := `"`+tc.name+`"`
				// Distinct fixed fields and dotted syntax independently witness that
				// this is projection, not JSON aggregation or a builtin alias.
				q := "SELECT "+quoted+"("+row+") AS projected,("+row+")."+quoted+" AS dotted"
				jsonArrayAggregateRoutes(t,p,b,fixture.Select(q,[]string{"projected","dotted"},[]string{"INT4","INT4"},[][]any{{tc.value,tc.value}}))
				jsonArrayAggregateRoutes(t,p,b,jsonArrayAggregateSelect("SELECT "+quoted+"(NULL::ja_quoted_projection) AS v","INT4",nil))
				q = "SELECT "+quoted+"($1::ja_quoted_projection) AS v"
				jsonArrayAggregateRoutes(t,p,b,jsonArrayAggregateSelect(q,"INT4",tc.value,"(7,8,9,10,11,12,13,14)"))
				jsonArrayAggregateRoutes(t,p,b,jsonArrayAggregateSelect(q,"INT4",nil,nil))
				for _, absent := range []struct{arg,signature string}{
					{"7","integer"},{"ROW(7)::ja_quoted_no_collision","ja_quoted_no_collision"},
				} {
					q = "SELECT "+quoted+"("+absent.arg+") AS v"
					d := fixture.ControlsReferenceDiagnostic("42883","function "+tc.name+"("+absent.signature+") does not exist")
					d.Hint,d.Position = hint,"8"
					jsonArrayAggregateError(t,p,b,q,d,true)
				}
			})
		}
	})
}

func TestReferenceJSONArrayAggregateDiagnostics(t *testing.T) {
	jsonArrayAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		jsonArrayAggregateSetup(t, p, b)
		jsonArrayAggregateSmokeDiagnostics(t, p, b)
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, name := range []string{"json_agg", "jsonb_agg", "json_agg_strict", "jsonb_agg_strict"} {
			for _, tc := range []struct{ args, signature, code, message string }{
				{"", "", "42883", ""}, {"*", "", "42883", ""}, {"1,2", "integer, integer", "42883", ""},
				{"NULL", "", "42804", "could not determine polymorphic type because input has type unknown"},
				{"'x'", "", "42804", "could not determine polymorphic type because input has type unknown"},
				{"$1", "", "42804", "could not determine polymorphic type because input has type unknown"},
			} {
				t.Run(name+"/"+tc.args, func(t *testing.T) {
					q := "SELECT "+name+"("+tc.args+") FROM ja_input"
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					if tc.code == "42883" { d.Message, d.Hint, d.Position = "function "+name+"("+tc.signature+") does not exist", hint, "8" }
					if tc.args == "$1" { jsonArrayAggregateError(t, p, b, q, d, true, "x") } else { jsonArrayAggregateError(t, p, b, q, d, true) }
				})
			}
		}
		for _, tc := range []struct{ q, code, message, token, detail, hint string }{
			{"SELECT json_agg(missing,1) FROM ja_input", "42703", `column "missing" does not exist`, "missing", "", ""},
			{"SELECT json_agg(v) FILTER(WHERE 1) FROM ja_input", "42804", "argument of FILTER must be type boolean, not type integer", "1)", "", ""},
			{"SELECT json_agg(v,1) FILTER(WHERE 2) FROM ja_input", "42804", "argument of FILTER must be type boolean, not type integer", "2)", "", ""},
			{"INSERT INTO ja_input(id,v) VALUES(8,json_agg(missing))", "42703", `column "missing" does not exist`, "missing", "", ""},
			{"INSERT INTO ja_input(id,v) VALUES(8,json_agg(1,2))", "42883", "function json_agg(integer, integer) does not exist", "json_agg", "", hint},
			{"INSERT INTO ja_input(id,v) VALUES(8,json_agg(1) FILTER(WHERE 2))", "42804", "argument of FILTER must be type boolean, not type integer", "2)", "", ""},
			{"SELECT \"JSON_AGG\"(v) FROM ja_input", "42883", "function JSON_AGG(integer) does not exist", "\"JSON_AGG\"", "", hint},
			{"SELECT json_agg(jsonb_agg(v)) FROM ja_input", "42803", "aggregate function calls cannot be nested", "jsonb_agg", "", ""},
			{"SELECT json_agg(v) FROM ja_input WHERE json_agg(v) IS NULL", "42803", "aggregate functions are not allowed in WHERE", "json_agg(v) IS", "", ""},
			{"SELECT v FROM ja_input GROUP BY json_agg(v)", "42803", "aggregate functions are not allowed in GROUP BY", "json_agg", "", ""},
			{"SELECT json_agg(j ORDER BY j) FROM ja_input", "42883", "could not identify an ordering operator for type json", "j)", "", "Use an explicit ordering operator or modify the query."},
			{"SELECT json_agg(DISTINCT j) FROM ja_input", "42883", "could not identify an equality operator for type json", "j)", "", ""},
			{"SELECT json_agg(DISTINCT v ORDER BY id) FROM ja_input", "42P10", "in an aggregate with DISTINCT, ORDER BY expressions must appear in argument list", "id)", "", ""},
			{"SELECT public.json_agg(v) FROM ja_input", "42883", "function public.json_agg(integer) does not exist", "public", "", hint},
			{"SELECT ja_missing.json_agg(v) FROM ja_input", "3F000", `schema "ja_missing" does not exist`, "ja_missing", "", ""},
			{"SELECT json_arrayagg(1 FORMAT JSON)", "42804", "cannot use non-string types with explicit FORMAT JSON clause", "FORMAT", "", ""},
			{"SELECT json_arrayagg('[1]'::text FORMAT JSON ENCODING UTF8)", "42804", "JSON ENCODING clause is only allowed for bytea input type", "FORMAT", "", ""},
			{"SELECT json_arrayagg('[1]'::json FORMAT JSON ENCODING UTF8)", "42804", "JSON ENCODING clause is only allowed for bytea input type", "FORMAT", "", ""},
			{"SELECT json_arrayagg(1 RETURNING text FORMAT JSON ENCODING UTF8)", "0A000", "cannot set JSON encoding for non-bytea output types", "FORMAT", "", ""},
			{"SELECT json_arrayagg(1 RETURNING json FORMAT JSON ENCODING UTF8)", "0A000", "cannot set JSON encoding for non-bytea output types", "FORMAT", "", ""},
			{"SELECT json_arrayagg(1 RETURNING jsonb FORMAT JSON ENCODING UTF8)", "0A000", "cannot set JSON encoding for non-bytea output types", "FORMAT", "", ""},
			{"SELECT json_arrayagg(1 RETURNING bytea FORMAT JSON ENCODING UTF32)", "0A000", "unsupported JSON encoding", "FORMAT", "", "Only UTF8 JSON encoding is supported."},
			{"SELECT json_arrayagg(1 RETURNING bytea FORMAT JSON ENCODING latin1)", "22023", "unrecognized JSON encoding: latin1", "latin1", "", ""},
			{"SELECT json_arrayagg(1 RETURNING integer)", "42846", "cannot cast type json to integer", "", "", ""},
			{"SELECT json_arrayagg(1 RETURNING bool FORMAT JSON)", "42846", "cannot cast type json to boolean", "FORMAT", "", ""},
			{"SELECT json_arrayagg(1 RETURNING record)", "0A000", "returning pseudo-types is not supported in SQL/JSON functions", "", "", ""},
			{"SELECT json_arrayagg(1 RETURNING anyelement)", "0A000", "returning pseudo-types is not supported in SQL/JSON functions", "", "", ""},
			{"SELECT json_arrayagg(1 RETURNING SETOF json)", "0A000", "returning SETOF types is not supported in SQL/JSON functions", "", "", ""},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Detail, d.Hint = tc.detail, tc.hint
				if tc.token != "" { d.Position = jsonArrayAggregatePosition(tc.q, tc.token) }
				jsonArrayAggregateError(t, p, b, tc.q, d, true)
			})
		}
		for _, tc := range []struct{ q, token string }{
			{"SELECT json_arrayagg()", ")"}, {"SELECT json_arrayagg(1,2)", ","},
			{"SELECT json_arrayagg(DISTINCT v) FROM ja_input", "DISTINCT"}, {"SELECT json_arrayagg(ALL v) FROM ja_input", "ALL"},
			{"SELECT json_arrayagg(1 FORMAT XML)", "FORMAT"}, {"SELECT json_arrayagg(1 RETURNING bytea ENCODING UTF8)", "ENCODING"},
			{"SELECT json_arrayagg(", ""},
		} {
			t.Run(tc.q, func(t *testing.T) {
				message := `syntax error at or near "`+tc.token+`"`
				position := jsonArrayAggregatePosition(tc.q, tc.token)
				if tc.token == "" { message, position = "syntax error at end of input", fixture.Position(len(tc.q)+1) }
				d := fixture.ControlsReferenceDiagnostic("42601", message); d.Position = position
				jsonArrayAggregateError(t, p, b, tc.q, d, true)
			})
		}
		// Root's pinned oracle observed execute-stage literal FORMAT and BYTEA
		// input-encoding failures, with absent Position and the full fields below.
		for _, tc := range []struct{ q, code, message, detail, context, token string; bind bool; args []any }{
			{`SELECT json_arrayagg('no'::text FORMAT JSON)`, "22P02", "invalid input syntax for type json", `Token "no" is invalid.`, "JSON data, line 1: no", "", false, nil},
			{`SELECT json_arrayagg($1::text FORMAT JSON)`, "22P02", "invalid input syntax for type json", `Token "no" is invalid.`, "JSON data, line 1: no", "", false, []any{"no"}},
			{`SELECT jsonb_agg('{"a":"\u0000","a":1}'::json)`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"a":"\u0000...`, "", false, nil},
			{`SELECT jsonb_agg('{"a":"\uDC00","a":1}'::json)`, "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: {"a":"\uDC00...`, "", false, nil},
			{`SELECT json_arrayagg('{"a":1e131072,"a":1}'::text FORMAT JSON RETURNING jsonb)`, "22003", "value overflows numeric format", "", "", "", false, nil},
			{`SELECT json_arrayagg('[1]'::bytea FORMAT JSON ENCODING UTF16)`, "22023", `invalid source encoding name "UTF16"`, "", "", "", false, nil},
			{`SELECT json_arrayagg('[1]'::bytea FORMAT JSON ENCODING UTF32)`, "22023", `invalid source encoding name "UTF32"`, "", "", "", false, nil},
			{`SELECT json_arrayagg($1::bytea FORMAT JSON)`, "22021", `invalid byte sequence for encoding "UTF8": 0xff`, "", "", "", false, []any{[]byte{0xff}}},
			{`SELECT json_arrayagg($1::bytea FORMAT JSON ENCODING UTF8)`, "22021", `invalid byte sequence for encoding "UTF8": 0x00`, "", "", "", false, []any{[]byte{0x00}}},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message); d.Detail, d.Context = tc.detail, tc.context
				if tc.token != "" { d.Position = jsonArrayAggregatePosition(tc.q, tc.token) }
				jsonArrayAggregateError(t, p, b, tc.q, d, tc.bind, tc.args...)
			})
		}
		jsonArrayAggregateRoutes(t,p,b,fixture.Select("SELECT id FROM ja_input ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)},{int64(3)},{int64(4)}}))
	})
}
