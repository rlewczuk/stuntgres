package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBStripNullsDiagnostics(t *testing.T) {
	jsonbStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ args, signature string }{
			{"", "jsonb_strip_nulls()"}, {"1", "jsonb_strip_nulls(integer)"}, {"'{}'::text", "jsonb_strip_nulls(text)"}, {"'{}'::json", "jsonb_strip_nulls(json)"},
			{"'{}',1", "jsonb_strip_nulls(unknown, integer)"}, {"'{}',true,false", "jsonb_strip_nulls(unknown, boolean, boolean)"},
			{"wrong => '{}'", "jsonb_strip_nulls(wrong => unknown)"}, {"strip_in_arrays => true", "jsonb_strip_nulls(strip_in_arrays => boolean)"},
			{"'{}',VARIADIC ARRAY[true]", "jsonb_strip_nulls(unknown, boolean[])"},
		} {
			t.Run(tc.args, func(t *testing.T) {
				jsonTypeAnalysisError(t, p, b, c, jsonTypeSignature("SELECT jsonb_strip_nulls("+tc.args+")", tc.signature, 8))
			})
		}
		for _, tc := range []struct{ query, code, message, at string }{
			{"SELECT jsonb_strip_nulls(target => '{}',target => '{}')", "42601", `argument name "target" used more than once`, "target => '{}'"},
			{"SELECT jsonb_strip_nulls(target => '{}',true)", "42601", "positional argument cannot follow named argument", "true"},
			{"SELECT jsonb_strip_nulls('{}',target => '{}')", "42883", "function jsonb_strip_nulls(unknown, target => unknown) does not exist", "jsonb_strip_nulls"},
			{`SELECT "JSONB_STRIP_NULLS"('{}')`, "42883", "function JSONB_STRIP_NULLS(unknown) does not exist", `"JSONB_STRIP_NULLS"`},
			{"SELECT jsonb_strip_nulls(*)", "42883", "function jsonb_strip_nulls() does not exist", "jsonb_strip_nulls"},
			{"SELECT jsonb_strip_nulls(DISTINCT '{}')", "42809", "DISTINCT specified, but jsonb_strip_nulls is not an aggregate function", "jsonb_strip_nulls"},
			{"SELECT jsonb_strip_nulls('{}' ORDER BY 1)", "42809", "ORDER BY specified, but jsonb_strip_nulls is not an aggregate function", "jsonb_strip_nulls"},
			{"SELECT jsonb_strip_nulls('{}') FILTER (WHERE true)", "42809", "FILTER specified, but jsonb_strip_nulls is not an aggregate function", "jsonb_strip_nulls"},
			{"SELECT jsonb_strip_nulls('{}') OVER ()", "42809", "OVER specified, but jsonb_strip_nulls is not a window function nor an aggregate function", "jsonb_strip_nulls"},
		} {
			t.Run(tc.query, func(t *testing.T) {
				index := strings.Index(tc.query, tc.at)
				if strings.Contains(tc.message, "used more than once") {
					index = strings.LastIndex(tc.query, tc.at)
				}
				s := jsonTypeError(tc.query, tc.code, tc.message, len([]rune(tc.query[:index]))+1)
				if tc.code == "42883" {
					s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				}
				jsonTypeAnalysisError(t, p, b, c, s)
			})
		}
		t.Run("character_position", func(t *testing.T) {
			jsonTypeAnalysisError(t, p, b, c, jsonTypeSignature("/*é*/ SELECT jsonb_strip_nulls(1)", "jsonb_strip_nulls(integer)", 14))
			q := "/*é*/ SELECT jsonb_strip_nulls('oops',NULL)"
			s := jsonTypeError(q, "22P02", "invalid input syntax for type json", 32)
			s.Diagnostic.Detail, s.Diagnostic.Context = `Token "oops" is invalid.`, "JSON data, line 1: oops"
			jsonTypeAnalysisError(t, p, b, c, s)
		})
		for _, suffix := range []string{"", ",NULL"} {
			t.Run("literal"+suffix, func(t *testing.T) {
				s := jsonTypeError("SELECT jsonb_strip_nulls('oops'"+suffix+")", "22P02", "invalid input syntax for type json", 26)
				s.Diagnostic.Detail, s.Diagnostic.Context = `Token "oops" is invalid.`, "JSON data, line 1: oops"
				jsonTypeAnalysisError(t, p, b, c, s)
			})
			t.Run("bound"+suffix, func(t *testing.T) {
				s := jsonTypeError("SELECT jsonb_strip_nulls($1"+suffix+")", "22P02", "invalid input syntax for type json", 0, "oops")
				s.Diagnostic.Detail = `Token "oops" is invalid.`
				s.Diagnostic.Context = "JSON data, line 1: oops\nunnamed portal parameter $1 = '...'"
				jsonTypeBoth(t, p, b, c, s)
			})
		}

		for _, operand := range []string{"NULL,missing_flag", "missing_target,NULL"} {
			q := "SELECT jsonb_strip_nulls(" + operand + ")"
			name := "missing_flag"
			if operand == "missing_target,NULL" {
				name = "missing_target"
			}
			at := strings.Index(q, name) + 1
			jsonTypeAnalysisError(t, p, b, c, jsonTypeError(q, "42703", `column "`+name+`" does not exist`, at))
		}
		for _, tc := range []struct{ input, code, message, detail, context string }{
			{`{"x":"\u0000"}`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"x":"\u0000...`},
			{`{"x":"\uD800"}`, "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: {"x":"\uD800"...`},
			{"1e131072", "22003", "value overflows numeric format", "", ""},
		} {
			t.Run(tc.input, func(t *testing.T) {
				for _, suffix := range []string{"", ",NULL"} {
					s := jsonTypeError("SELECT jsonb_strip_nulls("+jsonTypeQuote(tc.input)+suffix+")", tc.code, tc.message, 26)
					s.Diagnostic.Detail, s.Diagnostic.Context = tc.detail, tc.context
					jsonTypeAnalysisError(t, p, b, c, s)
					s = jsonTypeError("SELECT jsonb_strip_nulls($1"+suffix+")", tc.code, tc.message, 0, tc.input)
					s.Diagnostic.Detail, s.Diagnostic.Context = tc.detail, tc.context
					if s.Diagnostic.Context != "" {
						s.Diagnostic.Context += "\n"
					}
					s.Diagnostic.Context += "unnamed portal parameter $1 = '...'"
					jsonTypeBoth(t, p, b, c, s)
				}
			})
		}
		badFlag := jsonTypeError("SELECT jsonb_strip_nulls($1,$2)", "22P02", `invalid input syntax for type boolean: "truth"`, 0, "{}", "truth")
		badFlag.Diagnostic.Context = "unnamed portal parameter $2 = '...'"
		jsonTypeBoth(t, p, b, c, badFlag)
	})
}

func TestReferenceJSONBStripNullsImmutable(t *testing.T) {
	jsonbStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, prepared := range []bool{false, true} {
			name := "jbs_immutable_direct"
			if prepared {
				name = "jbs_immutable_prepared"
			}
			t.Run(name, func(t *testing.T) {
				run := func(s fixture.Step) {
					if prepared {
						s = fixture.Prepared(s)
					}
					jsonTypeRun(t, p, b, c, s)
				}
				run(fixture.Exec("CREATE TABLE "+name+"(id int,v jsonb,flag bool,g_default jsonb GENERATED ALWAYS AS (jsonb_strip_nulls(v)) STORED,g_flag jsonb GENERATED ALWAYS AS (jsonb_strip_nulls(v,flag)) STORED)", 0))
				run(fixture.Exec("CREATE INDEX "+name+"_expr ON "+name+" ((jsonb_strip_nulls(v,true)))", 0))
				run(fixture.Exec("CREATE INDEX "+name+"_pred ON "+name+`(id) WHERE jsonb_strip_nulls(v,true)='{"b":[1]}'::jsonb`, 0))
				run(fixture.Exec("INSERT INTO "+name+`(id,v,flag) VALUES(1,'{"a":null,"b":[null,1]}',true),(2,'null',true),(3,NULL,false),(4,'{"a":null,"b":[null,1]}',NULL)`, 4))
				want := [][]any{{int64(1), `{"b": [null, 1]}`, `{"b": [1]}`}, {int64(2), "null", "null"}, {int64(3), nil, nil}, {int64(4), `{"b": [null, 1]}`, nil}}
				rows := func() {
					run(fixture.Select("SELECT id,g_default,g_flag FROM "+name+" ORDER BY id", []string{"id", "g_default", "g_flag"}, []string{"INT4", "JSONB", "JSONB"}, want))
					for _, column := range []string{"g_default", "g_flag"} {
						expected := make([][]any, len(want))
						index := 1
						if column == "g_flag" {
							index = 2
						}
						for i := range want {
							expected[i] = []any{want[i][index]}
						}
						// Computed projections retain unknown nullability while
						// exercising the generated JSONB values and raw carriers.
						run(jsonbStripMetadata(fixture.Select("SELECT jsonb_strip_nulls("+column+") AS stripped FROM "+name+" ORDER BY id", []string{"stripped"}, []string{"JSONB"}, expected), b.PG))
					}
				}
				matches := func(ids [][]any) {
					for _, predicate := range []string{`jsonb_strip_nulls(v,true)='{"b":[1]}'::jsonb`, `'{"b":[1]}'::jsonb=jsonb_strip_nulls(v,true)`} {
						run(fixture.Select("SELECT id FROM "+name+" WHERE "+predicate+" ORDER BY id", []string{"id"}, []string{"INT4"}, ids))
					}
				}
				rows()
				matches([][]any{{int64(1)}, {int64(4)}})
				run(fixture.Exec("UPDATE "+name+` SET v='{"z":null,"b":[null,2]}',flag=false WHERE id=1`, 1))
				want[0] = []any{int64(1), `{"b": [null, 2]}`, `{"b": [null, 2]}`}
				rows()
				matches([][]any{{int64(4)}})
				run(fixture.Exec("UPDATE "+name+" SET flag=true WHERE id=4", 1))
				want[3][2] = `{"b": [1]}`
				rows()
				matches([][]any{{int64(4)}})
				for _, tc := range []struct{ query, message, reuse string }{
					{"CREATE TABLE " + name + "_bad_generated(v jsonb,g jsonb GENERATED ALWAYS AS (jsonb_strip_nulls(v,(random()>0.5)::boolean)) STORED)", "generation expression is not immutable", "CREATE TABLE " + name + "_bad_generated(v jsonb,g jsonb GENERATED ALWAYS AS (jsonb_strip_nulls(v,true)) STORED)"},
					{"CREATE INDEX " + name + "_bad_expr ON " + name + " ((jsonb_strip_nulls(v,(random()>0.5)::boolean)))", "functions in index expression must be marked IMMUTABLE", "CREATE INDEX " + name + "_bad_expr ON " + name + " ((jsonb_strip_nulls(v,true)))"},
					{"CREATE INDEX " + name + "_bad_pred ON " + name + `(id) WHERE jsonb_strip_nulls(v,(random()>0.5)::boolean)='{}'::jsonb`, "functions in index predicate must be marked IMMUTABLE", "CREATE INDEX " + name + "_bad_pred ON " + name + `(id) WHERE jsonb_strip_nulls(v,true)='{}'::jsonb`},
				} {
					s := jsonTypeError(tc.query, "42P17", tc.message, 0)
					s.SelectRows = false
					run(s)
					rows()
					run(fixture.Exec(tc.reuse, 0))
				}
				run(fixture.Select("SELECT count(*) AS n FROM "+name+"_bad_generated", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			})
		}
	})
}
