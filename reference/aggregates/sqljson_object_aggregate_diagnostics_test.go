package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceSQLJSONObjectAggregateGrammar(t *testing.T) {
	sqlJSONObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ call, token string }{
			{"json_objectagg()", ")"}, {"json_objectagg(1,2)", ","}, {"json_objectagg(1:2,3:4)", ","},
			{"json_objectagg(DISTINCT k:v)", "DISTINCT"}, {"json_objectagg(ALL k:v)", "ALL"},
			{"json_objectagg(VARIADIC k:v)", "VARIADIC"}, {"json_objectagg(KEY k VALUE v)", "k"},
			{"json_objectagg(k:v ORDER BY k)", "ORDER"}, {"json_objectagg('a':)", ")"},
			{"json_objectagg('a':1 WITH UNIQUE KEY)", "KEY"},
			{"json_objectagg('a':1 ABSENT ON NULL NULL ON NULL)", "NULL ON NULL)"},
			{"json_objectagg('a':1 WITH UNIQUE ABSENT ON NULL)", "ABSENT"},
			{"json_objectagg('a':1 RETURNING)", ")"},
			{"json_objectagg('a':1 RETURNING json WITH UNIQUE)", "WITH"},
			{"json_objectagg('a':1 FORMAT XML)", "FORMAT"},
			{"json_objectagg('a':1 RETURNING bytea ENCODING UTF8)", "ENCODING"},
			{"json_objectagg(1+2 VALUE 3)", "VALUE"},
			{"json_objectagg('a'::text VALUE 1)", "VALUE"},
			{`pg_catalog.json_objectagg('a':1)`, ":"},
			{`"json_objectagg"('a':1)`, ":"},
		} {
			t.Run(tc.call, func(t *testing.T) {
				q := "/*é😀*/ SELECT " + tc.call
				token := tc.token
				if token == "NULL ON NULL)" {
					token = "NULL"
				}
				d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "`+token+`"`)
				d.Position = jsonArrayAggregatePosition(q, tc.token)
				jsonArrayAggregateError(t, p, b, q, d, true)
			})
		}
		for _, q := range []string{"/*é😀*/ SELECT json_objectagg(", "/*é😀*/ SELECT json_objectagg('a':1 RETURNING", "/*é😀*/ SELECT json_objectagg('a':"} {
			t.Run(q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
				d.Position = fixture.Position(utf8.RuneCountInString(q) + 1)
				jsonArrayAggregateError(t, p, b, q, d, true)
			})
		}
		q := "/*é😀*/ SELECT json_objectagg('a':1) AS v garbage"
		d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "garbage"`)
		d.Position = jsonArrayAggregatePosition(q, "garbage")
		jsonArrayAggregateError(t, p, b, q, d, true)
		for _, tc := range []struct{ pair, want string }{
			{"1+2:3", `{ "3" : 3 }`}, {"(1+2) VALUE 3", `{ "3" : 3 }`},
			{"('a'::text) VALUE 1", `{ "a" : 1 }`},
		} {
			t.Run(tc.pair, func(t *testing.T) {
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT json_objectagg("+tc.pair+") AS v", "JSON", tc.want))
			})
		}
		// Context words remain ordinary identifiers outside a special call and
		// nested calls must not consume their enclosing call's option scope.
		for _, q := range []string{
			"SELECT json_objectagg(value:format WITH UNIQUE KEYS) AS v FROM (VALUES ('a',1)) t(value,format)",
			"SELECT json_objectagg(keys:encoding) AS v FROM (VALUES ('a',1)) t(keys,encoding)",
			"SELECT json_objectagg('a':(SELECT json_arrayagg(1))) AS v",
		} {
			t.Run(q, func(t *testing.T) {
				want := `{ "a" : 1 }`
				if q == "SELECT json_objectagg('a':(SELECT json_arrayagg(1))) AS v" {
					want = `{ "a" : [1] }`
				}
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, "JSON", want))
			})
		}
	})
}

func TestReferenceSQLJSONObjectAggregateDiagnostics(t *testing.T) {
	sqlJSONObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ expr, code, message, token, hint string }{
			{"'a':1 FORMAT JSON", "42804", "cannot use non-string types with explicit FORMAT JSON clause", "FORMAT", ""},
			{"'a':'[1]'::text FORMAT JSON ENCODING UTF8", "42804", "JSON ENCODING clause is only allowed for bytea input type", "FORMAT", ""},
			{"'a':'[1]'::json FORMAT JSON ENCODING UTF8", "42804", "JSON ENCODING clause is only allowed for bytea input type", "FORMAT", ""},
			{"'a':'[1]'::jsonb FORMAT JSON ENCODING UTF8", "42804", "JSON ENCODING clause is only allowed for bytea input type", "FORMAT", ""},
			{"'a':1 RETURNING text FORMAT JSON ENCODING UTF8", "0A000", "cannot set JSON encoding for non-bytea output types", "FORMAT", ""},
			{"'a':1 RETURNING json FORMAT JSON ENCODING UTF8", "0A000", "cannot set JSON encoding for non-bytea output types", "FORMAT", ""},
			{"'a':1 RETURNING jsonb FORMAT JSON ENCODING UTF8", "0A000", "cannot set JSON encoding for non-bytea output types", "FORMAT", ""},
			{"'a':1 RETURNING bytea FORMAT JSON ENCODING UTF16", "0A000", "unsupported JSON encoding", "FORMAT", "Only UTF8 JSON encoding is supported."},
			{"'a':1 RETURNING bytea FORMAT JSON ENCODING UTF32", "0A000", "unsupported JSON encoding", "FORMAT", "Only UTF8 JSON encoding is supported."},
			{"'a':1 RETURNING bytea FORMAT JSON ENCODING latin1", "22023", "unrecognized JSON encoding: latin1", "latin1", ""},
			{"'a':'[1]'::bytea FORMAT JSON ENCODING latin1", "22023", "unrecognized JSON encoding: latin1", "latin1", ""},
			{`'a':1 RETURNING bytea FORMAT JSON ENCODING "Latin1"`, "22023", "unrecognized JSON encoding: Latin1", `"Latin1"`, ""},
			{"'a':1 RETURNING integer", "42846", "cannot cast type json to integer", "", ""},
			{"'a':1 RETURNING bool FORMAT JSON", "42846", "cannot cast type json to boolean", "FORMAT", ""},
			{"'a':1 RETURNING record", "0A000", "returning pseudo-types is not supported in SQL/JSON functions", "", ""},
			{"'a':1 RETURNING anyelement", "0A000", "returning pseudo-types is not supported in SQL/JSON functions", "", ""},
			{"'a':1 RETURNING SETOF json", "0A000", "returning SETOF types is not supported in SQL/JSON functions", "", ""},
			{"'a':1 RETURNING sjo_missing_type", "42704", `type "sjo_missing_type" does not exist`, "sjo_missing_type", ""},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				q := "/*é😀*/ SELECT json_objectagg(" + tc.expr + ") AS v"
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Hint = tc.hint
				if tc.token != "" {
					d.Position = jsonArrayAggregatePosition(q, tc.token)
				}
				jsonArrayAggregateError(t, p, b, q, d, true)
			})
		}
		for _, tc := range []struct {
			expr, code, message, detail, context string
			args                                 []any
		}{
			{"'a':'no'::text FORMAT JSON", "22P02", "invalid input syntax for type json", `Token "no" is invalid.`, "JSON data, line 1: no", nil},
			{"'a':$1::text FORMAT JSON", "22P02", "invalid input syntax for type json", `Token "no" is invalid.`, "JSON data, line 1: no", []any{"no"}},
			{"'a':'[1]'::bytea FORMAT JSON ENCODING UTF16", "22023", `invalid source encoding name "UTF16"`, "", "", nil},
			{"'a':'[1]'::bytea FORMAT JSON ENCODING UTF32", "22023", `invalid source encoding name "UTF32"`, "", "", nil},
			{"'a':$1::bytea FORMAT JSON", "22021", `invalid byte sequence for encoding "UTF8": 0xff`, "", "", []any{[]byte{0xff}}},
			{"'a':$1::bytea FORMAT JSON ENCODING UTF8", "22021", `invalid byte sequence for encoding "UTF8": 0x00`, "", "", []any{[]byte{0x00}}},
			{"NULL:1", "22004", "null value not allowed for object key", "", "", nil},
			{"NULL:1 RETURNING jsonb", "22023", "field name must not be null", "", "", nil},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				q := "SELECT json_objectagg(" + tc.expr + ") AS v"
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Detail, d.Context = tc.detail, tc.context
				jsonArrayAggregateError(t, p, b, q, d, false, tc.args...)
			})
		}
		q := "/*é😀*/ SELECT jsonb_objectagg('a',1)"
		d := fixture.ControlsReferenceDiagnostic("42883", "function jsonb_objectagg(unknown, integer) does not exist")
		d.Position = jsonArrayAggregatePosition(q, "jsonb_objectagg")
		d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		jsonArrayAggregateError(t, p, b, q, d, true)
	})
}

func TestReferenceSQLJSONObjectAggregatePrecedence(t *testing.T) {
	sqlJSONObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct {
			q, code, message, token string
			bind                    bool
		}{
			{"SELECT json_objectagg(missing:1 FORMAT JSON RETURNING sjo_missing_type) FILTER(WHERE 1)", "42703", `column "missing" does not exist`, "missing", true},
			{"SELECT json_objectagg('a':missing RETURNING sjo_missing_type) FILTER(WHERE 1)", "42703", `column "missing" does not exist`, "missing", true},
			{"SELECT json_objectagg('a':1 FORMAT JSON RETURNING sjo_missing_type) FILTER(WHERE 1)", "42804", "cannot use non-string types with explicit FORMAT JSON clause", "FORMAT", true},
			{"SELECT json_objectagg('a':1 RETURNING integer) FILTER(WHERE 1)", "42804", "argument of FILTER must be type boolean, not type integer", "1)", true},
			{"SELECT json_objectagg('a':2) FILTER(WHERE 1)", "42804", "argument of FILTER must be type boolean, not type integer", "1)", true},
			{"SELECT json_objectagg(NULL:'no'::text FORMAT JSON)", "22P02", "invalid input syntax for type json", "", false},
			{"SELECT json_objectagg(ARRAY[1]:1/(n-n) ABSENT ON NULL) FROM (VALUES (1)) t(n)", "22012", "division by zero", "", false},
			{"SELECT json_objectagg('a':json_objectagg('b':1))", "42803", "aggregate function calls cannot be nested", "json_objectagg('b'", true},
			{"SELECT 1 WHERE json_objectagg('a':1) IS NULL", "42803", "aggregate functions are not allowed in WHERE", "json_objectagg", true},
			{"SELECT 1 GROUP BY json_objectagg('a':1)", "42803", "aggregate functions are not allowed in GROUP BY", "json_objectagg", true},
			{"CREATE TABLE sjo_bad_default(v json DEFAULT json_objectagg('a':1))", "42803", "aggregate functions are not allowed in DEFAULT expressions", "json_objectagg", true},
			{"CREATE TABLE sjo_bad_check(v int CHECK(json_objectagg('a':v) IS NULL))", "42803", "aggregate functions are not allowed in check constraints", "json_objectagg", true},
			{"CREATE TABLE sjo_bad_generated(v int,j json GENERATED ALWAYS AS (json_objectagg('a':v)) STORED)", "42803", "aggregate functions are not allowed in column generation expressions", "json_objectagg", true},
		} {
			t.Run(tc.q, func(t *testing.T) {
				q := tc.q
				if len(q) >= 7 && q[:7] == "SELECT " {
					q = "/*é😀*/ " + q
				}
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				if tc.token != "" {
					d.Position = jsonArrayAggregatePosition(q, tc.token)
				}
				if tc.code == "22P02" {
					d.Detail, d.Context = `Token "no" is invalid.`, "JSON data, line 1: no"
				}
				jsonArrayAggregateError(t, p, b, q, d, tc.bind)
			})
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE sjo_placement(v int,j json)", 0))
		for _, tc := range []struct{ q, message, token string }{
			{"INSERT INTO sjo_placement(j) VALUES(json_objectagg('a':1))", "aggregate functions are not allowed in VALUES", "json_objectagg"},
			{"INSERT INTO sjo_placement(v) VALUES(1) RETURNING json_objectagg('a':v)", "aggregate functions are not allowed in RETURNING", "json_objectagg"},
			{"CREATE INDEX sjo_bad_index ON sjo_placement ((json_objectagg('a':v)))", "aggregate functions are not allowed in index expressions", "json_objectagg"},
			{"INSERT INTO sjo_placement(j) VALUES(json_objectagg(missing:1))", `column "missing" does not exist`, "missing"},
			{"INSERT INTO sjo_placement(j) VALUES(json_objectagg('a':missing))", `column "missing" does not exist`, "missing"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				code := "42803"
				if tc.token == "missing" {
					code = "42703"
				}
				q := tc.q
				if len(q) >= 7 && q[:7] == "INSERT " {
					q = "/*é😀*/ " + q
				}
				d := fixture.ControlsReferenceDiagnostic(code, tc.message)
				d.Position = jsonArrayAggregatePosition(q, tc.token)
				jsonArrayAggregateError(t, p, b, q, d, true)
			})
		}
	})
}
