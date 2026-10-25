package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceTablespaceInfoDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	tablespaceInfoBackends(t, p, func(t *testing.T, b fixture.Backend) {
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		type sample struct {
			name, q, code, message, hint, at, context string
			args                                      []any
		}
		var cases []sample
		for _, name := range []string{"pg_tablespace_location", "pg_tablespace_databases"} {
			noMatch := func(label, call, signature string) {
				cases = append(cases, sample{name: name + "/" + label, q: "SELECT " + call, code: "42883", message: "function " + signature + " does not exist", hint: hint, at: call})
			}
			noMatch("zero", name+"()", name+"()")
			noMatch("two", name+"(1,2)", name+"(integer, integer)")
			noMatch("bool", name+"(true)", name+"(boolean)")
			noMatch("text", name+"('1663'::text)", name+"(text)")
			noMatch("manual_label", name+"(tablespace => 1663)", name+"(tablespace => integer)")
			noMatch("schema", "public."+name+"(1663)", "public."+name+"(integer)")
			noMatch("case", `"`+strings.ToUpper(name)+`"(1663)`, strings.ToUpper(name)+"(integer)")
			noMatch("missing", name+"_missing(1663)", name+"_missing(integer)")
			cases = append(cases,
				sample{name: name + "/unicode", q: "SELECT /* é界 */ " + name + "(true)", code: "42883", message: "function " + name + "(boolean) does not exist", hint: hint, at: name},
				sample{name: name + "/literal", q: "SELECT " + name + "('bad')", code: "22P02", message: `invalid input syntax for type oid: "bad"`, at: "'bad'"},
				sample{name: name + "/overflow", q: "SELECT " + name + "('4294967296')", code: "22003", message: `value "4294967296" is out of range for type oid`, at: "'4294967296'"},
				sample{name: name + "/parameter", q: "SELECT " + name + "($1)", code: "22P02", message: `invalid input syntax for type oid: "bad"`, context: `unnamed portal parameter $1 = '...'`, args: []any{"bad"}},
				sample{name: name + "/parameter_overflow", q: "SELECT " + name + "($1)", code: "22003", message: `value "4294967296" is out of range for type oid`, context: `unnamed portal parameter $1 = '...'`, args: []any{"4294967296"}},
				sample{name: name + "/star", q: "SELECT " + name + "(*)", code: "42883", message: "function " + name + "() does not exist", hint: hint, at: name},
				sample{name: name + "/distinct", q: "SELECT " + name + "(DISTINCT 1663)", code: "42809", message: "DISTINCT specified, but " + name + " is not an aggregate function", at: name},
				sample{name: name + "/order", q: "SELECT " + name + "(1663 ORDER BY 1)", code: "42809", message: "ORDER BY specified, but " + name + " is not an aggregate function", at: name},
				sample{name: name + "/filter", q: "SELECT " + name + "(1663) FILTER (WHERE true)", code: "42809", message: "FILTER specified, but " + name + " is not an aggregate function", at: name},
				sample{name: name + "/over", q: "SELECT " + name + "(1663) OVER ()", code: "42809", message: "OVER specified, but " + name + " is not a window function nor an aggregate function", at: name},
				sample{name: name + "/argument_priority", q: "SELECT " + name + "(DISTINCT true)", code: "42883", message: "function " + name + "(boolean) does not exist", hint: hint, at: name},
				sample{name: name + "/argument_limit", q: "SELECT " + name + "(" + strings.Repeat("1,", 100) + "1)", code: "54023", message: "cannot pass more than 100 arguments to a function", at: name},
			)
		}
		cases = append(cases,
			sample{name: "where", q: "SELECT 1 WHERE pg_tablespace_databases(1663) IS NOT NULL", code: "0A000", message: "set-returning functions are not allowed in WHERE", at: "pg_tablespace_databases"},
			sample{name: "where_argument_priority", q: "SELECT 1 WHERE pg_tablespace_databases(true) IS NOT NULL", code: "42883", message: "function pg_tablespace_databases(boolean) does not exist", hint: hint, at: "pg_tablespace_databases"},
			sample{name: "nested_from", q: "SELECT * FROM generate_series(1,pg_tablespace_databases(1663)::integer)", code: "0A000", message: "set-returning functions must appear at top level of FROM", at: "pg_tablespace_databases"},
		)
		for _, tc := range cases {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%t", tc.name, prepared), func(t *testing.T) {
					position := ""
					if tc.at != "" {
						index := strings.Index(tc.q, tc.at)
						if index < 0 {
							t.Fatal("missing source anchor")
						}
						position = fixture.Position(utf8.RuneCountInString(tc.q[:index]) + 1)
					}
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: position, Context: tc.context}, tc.args...)
					s.SelectRows = true
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared && len(tc.args) == 0 {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				})
			}
		}
		// Retain one prepared owner through invalid Bind input and valid recovery.
		q := "SELECT pg_tablespace_location($1) AS v"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Context: `unnamed portal parameter $1 = '...'`}, "bad")
		s.SelectRows = true
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, tablespaceInfoSelect(q, "TEXT", [][]any{{""}}, int64(1663)))
	})
}
