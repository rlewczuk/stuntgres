package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func inspectDiagnostic(query, code, message, hint, at, detail, context string) fixture.Diagnostic {
	d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Hint: hint, Detail: detail, Context: context}
	if at != "" {
		d.Position = strconv.Itoa(utf8.RuneCountInString(query[:strings.Index(query, at)]) + 1)
	}
	return d
}
func TestReferenceRangeInspectDiagnostics(t *testing.T) {
	inspectScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE inspect_types(a int4range,b int8range,m int4multirange,d daterange)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO inspect_types VALUES (NULL,NULL,NULL,NULL)", 1))
		const missing = "No function matches the given name and argument types. You might need to add explicit type casts."
		const ambiguous = "Could not choose a best candidate function. You might need to add explicit type casts."
		cases := []struct {
			q, code, message, hint, at, detail, context string
			args                                        []any
		}{
			{"SELECT isempty(1)", "42883", "function isempty(integer) does not exist", missing, "isempty", "", "", nil},
			{"SELECT lower(1)", "42883", "function lower(integer) does not exist", missing, "lower", "", "", nil},
			{"SELECT isempty()", "42883", "function isempty() does not exist", missing, "isempty", "", "", nil},
			{"SELECT isempty(*)", "42883", "function isempty() does not exist", missing, "isempty", "", "", nil},
			{"SELECT isempty(a,a) FROM inspect_types", "42883", "function isempty(int4range, int4range) does not exist", missing, "isempty", "", "", nil},
			{"SELECT range_merge(a) FROM inspect_types", "42883", "function range_merge(int4range) does not exist", missing, "range_merge", "", "", nil},
			{"SELECT range_merge(a,b) FROM inspect_types", "42883", "function range_merge(int4range, int8range) does not exist", missing, "range_merge", "", "", nil},
			{"SELECT range_merge(a,m) FROM inspect_types", "42883", "function range_merge(int4range, int4multirange) does not exist", missing, "range_merge", "", "", nil},
			{"SELECT range_merge(m,m) FROM inspect_types", "42883", "function range_merge(int4multirange, int4multirange) does not exist", missing, "range_merge", "", "", nil},
			{"SELECT isempty(x => a) FROM inspect_types", "42883", "function isempty(x => int4range) does not exist", missing, "isempty", "", "", nil},
			{`SELECT "ISEMPTY"(a) FROM inspect_types`, "42883", "function ISEMPTY(int4range) does not exist", missing, `"ISEMPTY"`, "", "", nil},
			{"SELECT absent.isempty(a) FROM inspect_types", "3F000", `schema "absent" does not exist`, "", "absent", "", "", nil},
			{"SELECT isempty(DISTINCT a) FROM inspect_types", "42809", "DISTINCT specified, but isempty is not an aggregate function", "", "isempty", "", "", nil},
			{"SELECT isempty(a ORDER BY 1) FROM inspect_types", "42809", "ORDER BY specified, but isempty is not an aggregate function", "", "isempty", "", "", nil},
			{"SELECT isempty(a) FILTER (WHERE true) FROM inspect_types", "42809", "FILTER specified, but isempty is not an aggregate function", "", "isempty", "", "", nil},
			{"SELECT isempty(a) OVER () FROM inspect_types", "42809", "OVER specified, but isempty is not a window function nor an aggregate function", "", "isempty", "", "", nil},
			{"SELECT /*é*/ isempty(missing)", "42703", `column "missing" does not exist`, "", "missing", "", "", nil},
			{"SELECT range_merge(a,'[4,1)') FROM inspect_types", "22000", "range lower bound must be less than or equal to range upper bound", "", "'[4,1)'", "", "", nil},
			{"SELECT range_merge(a,'[1,2147483647]') FROM inspect_types", "22003", "integer out of range", "", "'[1,2147483647]'", "", "", nil},
			{"SELECT range_merge(b,'[1,9223372036854775807]') FROM inspect_types", "22003", "bigint out of range", "", "'[1,9223372036854775807]'", "", "", nil},
			{"SELECT range_merge(d,'[2000-01-01,5874897-12-31]') FROM inspect_types", "22008", "date out of range", "", "'[2000-01-01,5874897-12-31]'", "", "", nil},
			{"SELECT range_merge(a,'bad') FROM inspect_types", "22P02", `malformed range literal: "bad"`, "", "'bad'", "Missing left parenthesis or bracket.", "", nil},
			{"SELECT range_merge(a,$1) FROM inspect_types", "22000", "range lower bound must be less than or equal to range upper bound", "", "", "", "unnamed portal parameter $1 = '...'", []any{"[4,1)"}},
			{"SELECT range_merge(a,$1) FROM inspect_types", "22P02", `malformed range literal: "bad"`, "", "", "Missing left parenthesis or bracket.", "unnamed portal parameter $1 = '...'", []any{"bad"}},
		}
		for _, name := range []string{"isempty", "lower_inc", "upper_inc", "lower_inf", "upper_inf"} {
			for _, input := range []string{"NULL", "'empty'"} {
				cases = append(cases, struct {
					q, code, message, hint, at, detail, context string
					args                                        []any
				}{"SELECT " + name + "(" + input + ")", "42725", "function " + name + "(unknown) is not unique", ambiguous, name, "", "", nil})
			}
		}
		for _, input := range []string{"NULL", "'empty'", "NULL,NULL", "'empty','empty'"} {
			cases = append(cases, struct {
				q, code, message, hint, at, detail, context string
				args                                        []any
			}{"SELECT range_merge(" + input + ")", "42804", "could not determine polymorphic type because input has type unknown", "", "", "", "", nil})
		}
		tooMany := "SELECT isempty(" + strings.TrimSuffix(strings.Repeat("a,", 101), ",") + ") FROM inspect_types"
		cases = append(cases, struct {
			q, code, message, hint, at, detail, context string
			args                                        []any
		}{tooMany, "54023", "cannot pass more than 100 arguments to a function", "", "isempty", "", "", nil})
		child := "SELECT isempty(missing," + strings.TrimSuffix(strings.Repeat("a,", 100), ",") + ") FROM inspect_types"
		cases = append(cases, struct {
			q, code, message, hint, at, detail, context string
			args                                        []any
		}{child, "42703", `column "missing" does not exist`, "", "missing", "", "", nil})
		for _, tc := range cases {
			t.Run(tc.q, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(tc.q, inspectDiagnostic(tc.q, tc.code, tc.message, tc.hint, tc.at, tc.detail, tc.context))
					s.SelectRows = true
					s.Prepared = prepared
					s.Args = tc.args
					s.ErrorStage = "execute"
					if prepared && tc.args == nil {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			})
		}
		inspectRunBoth(t, p, b, inspectSelect(b, "SELECT isempty(VARIADIC a) AS empty FROM inspect_types", []string{"empty"}, []string{"BOOL"}, [][]any{{nil}}))
		for _, prepared := range []bool{false, true} {
			q := "INSERT INTO inspect_types(m) VALUES ('bad')"
			s := fixture.ErrorDiagnostic(q, inspectDiagnostic(q, "22P02", `malformed multirange literal: "bad"`, "", "'bad'", "Missing left brace.", ""))
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		inspectRunBoth(t, p, b, inspectSelect(b, "SELECT lower(m) AS lo,isempty(m) AS empty,range_merge(m) AS hull FROM inspect_types", []string{"lo", "empty", "hull"}, []string{"INT4", "BOOL", "INT4RANGE"}, [][]any{{nil, nil, nil}}))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(id integer,v int4range)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES (0,'empty')", 1))
		q := "INSERT INTO r VALUES (1,'[1,2)'),(2,'[4,1)')"
		s := fixture.ErrorDiagnostic(q, inspectDiagnostic(q, "22000", "range lower bound must be less than or equal to range upper bound", "", "'[4,1)'", "", ""))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		inspectRunBoth(t, p, b, inspectSelect(b, "SELECT lower(v) AS lo,isempty(v) AS empty,range_merge(v,v) AS hull FROM r", []string{"lo", "empty", "hull"}, []string{"INT4", "BOOL", "INT4RANGE"}, [][]any{{nil, true, "empty"}}))
	})
}

// Review regressions share one public body and independent PostgreSQL
// diagnostics across both ordinary backend modes.
func TestReferenceRangeInspectDiagnosticOrdering(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE inspect_order(a int4range,m int4multirange,t text,n integer)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO inspect_order VALUES ('[1,4]','{[1,3)}','AbC',1)", 1))
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		type diagnosticCase struct {
			q, code, message, hint, at string
			last                       bool
		}
		cases := []diagnosticCase{}
		for _, name := range []string{"lower", "upper"} {
			for _, operand := range []struct{ expr, typ string }{{"'AbC'", "unknown"}, {"t", "text"}, {"a", "int4range"}, {"m", "int4multirange"}} {
				prefix, suffix := "SELECT ", " FROM inspect_order"
				cases = append(cases,
					diagnosticCase{prefix + name + "(x => " + operand.expr + ")" + suffix, "42883", "function " + name + "(x => " + operand.typ + ") does not exist", hint, name, false},
					diagnosticCase{prefix + name + "(DISTINCT " + operand.expr + ")" + suffix, "42809", "DISTINCT specified, but " + name + " is not an aggregate function", "", name, false},
					diagnosticCase{prefix + name + "(" + operand.expr + " ORDER BY 1)" + suffix, "42809", "ORDER BY specified, but " + name + " is not an aggregate function", "", name, false},
					diagnosticCase{prefix + name + "(" + operand.expr + ") FILTER (WHERE false)" + suffix, "42809", "FILTER specified, but " + name + " is not an aggregate function", "", name, false},
					diagnosticCase{prefix + name + "(" + operand.expr + ") OVER ()" + suffix, "42809", "OVER specified, but " + name + " is not a window function nor an aggregate function", "", name, false},
				)
			}
			for _, expr := range []string{"'AbC'", "t"} {
				value := "abc"
				if name == "upper" {
					value = "ABC"
				}
				inspectRunBoth(t, p, b, inspectSelect(b, "SELECT "+name+"("+expr+") AS v FROM inspect_order", []string{"v"}, []string{"TEXT"}, [][]any{{value}}))
			}
			inspectRunBoth(t, p, b, inspectSelect(b, "SELECT "+name+"(NULL) AS v FROM inspect_order", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
			parameter := inspectSelect(b, "SELECT "+name+"($1) AS v FROM inspect_order", []string{"v"}, []string{"TEXT"}, [][]any{{"abc"}})
			if name == "upper" {
				parameter.Rows = [][]any{{"ABC"}}
			}
			parameter.Args = []any{"AbC"}
			inspectRunBoth(t, p, b, parameter)
			bound := int64(1)
			if name == "upper" {
				bound = 5
			}
			inspectRunBoth(t, p, b, inspectSelect(b, "SELECT "+name+"(a) AS v FROM inspect_order", []string{"v"}, []string{"INT4"}, [][]any{{bound}}))
			cases = append(cases,
				diagnosticCase{"SELECT " + name + "(x => 'AbC',x => 'AbC')", "42601", `argument name "x" used more than once`, "", "x", true},
				diagnosticCase{"SELECT " + name + "(x => 'AbC','AbC')", "42601", "positional argument cannot follow named argument", "", "'AbC'", true},
			)
		}
		for _, call := range []struct{ name, args string }{
			{"lower", "t"}, {"upper", "'AbC'"}, {"isempty", "a"}, {"lower_inc", "a"}, {"upper_inc", "a"}, {"lower_inf", "a"}, {"upper_inf", "a"}, {"range_merge", "a,a"},
		} {
			ordinary := "SELECT " + call.name + "(" + call.args + ")"
			qualified := "SELECT absent." + call.name + "(" + call.args + ")"
			suffix := " FROM inspect_order"
			cases = append(cases,
				diagnosticCase{"SELECT /*é*/ absent." + call.name + "(missing)" + suffix, "42703", `column "missing" does not exist`, "", "missing", false},
				diagnosticCase{qualified + " FILTER (WHERE missing)" + suffix, "42703", `column "missing" does not exist`, "", "missing", false},
				diagnosticCase{qualified + " FILTER (WHERE 1)" + suffix, "42804", "argument of FILTER must be type boolean, not type integer", "", "1", false},
				diagnosticCase{qualified + " FILTER (WHERE true)" + suffix, "3F000", `schema "absent" does not exist`, "", "absent", false},
				diagnosticCase{ordinary + " FILTER (WHERE missing)" + suffix, "42703", `column "missing" does not exist`, "", "missing", false},
				diagnosticCase{ordinary + " FILTER (WHERE n)" + suffix, "42804", "argument of FILTER must be type boolean, not type integer", "", "n)", false},
				diagnosticCase{ordinary + " FILTER (WHERE true)" + suffix, "42809", "FILTER specified, but " + call.name + " is not an aggregate function", "", call.name, false},
			)
		}
		cases = append(cases,
			diagnosticCase{"SELECT lower(missing_arg) FILTER (WHERE missing_filter) FROM inspect_order", "42703", `column "missing_arg" does not exist`, "", "missing_arg", false},
			diagnosticCase{"SELECT range_merge(a,missing_arg) FILTER (WHERE missing_filter) FROM inspect_order", "42703", `column "missing_arg" does not exist`, "", "missing_arg", false},
			diagnosticCase{"SELECT lower(1) FILTER (WHERE missing)", "42703", `column "missing" does not exist`, "", "missing", false},
			diagnosticCase{"SELECT isempty() FILTER (WHERE missing)", "42703", `column "missing" does not exist`, "", "missing", false},
			diagnosticCase{"SELECT range_merge(NULL) FILTER (WHERE 1)", "42804", "argument of FILTER must be type boolean, not type integer", "", "1", false},
			diagnosticCase{"SELECT lower(1) FILTER (WHERE true)", "42883", "function lower(integer) does not exist", hint, "lower", false},
			diagnosticCase{"SELECT pg_catalog.lower(DISTINCT t) FROM inspect_order", "42809", "DISTINCT specified, but pg_catalog.lower is not an aggregate function", "", "pg_catalog", false},
		)
		wide := strings.TrimSuffix(strings.Repeat("a,", 101), ",")
		cases = append(cases,
			diagnosticCase{"SELECT absent.isempty(" + wide + ") FILTER (WHERE missing) FROM inspect_order", "42703", `column "missing" does not exist`, "", "missing", false},
			diagnosticCase{"SELECT absent.isempty(" + wide + ") FROM inspect_order", "54023", "cannot pass more than 100 arguments to a function", "", "absent", false},
		)
		for _, tc := range cases {
			t.Run(tc.q, func(t *testing.T) {
				want := inspectDiagnostic(tc.q, tc.code, tc.message, tc.hint, tc.at, "", "")
				if tc.last {
					want.Position = strconv.Itoa(utf8.RuneCountInString(tc.q[:strings.LastIndex(tc.q, tc.at)]) + 1)
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(tc.q, want)
					s.SelectRows = true
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			})
		}
	}
}
