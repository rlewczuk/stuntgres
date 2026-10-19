package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceBooleanCallDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE bcp_empty(id oid)", 0))
			run(fixture.Exec("CREATE TABLE bcp_sentinel(id int)", 0))
			check := func(q string, d fixture.Diagnostic, args ...any) {
				t.Helper()
				t.Run(q, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError(q, d, args...)
						s.SelectRows = true
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared && len(args) == 0 {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
					}
				})
			}
			for _, tc := range []struct{ q, construct string }{
				{"SELECT 1 WHERE num_nulls(NULL)", "WHERE"},
				{"SELECT 1 WHERE num_nulls(NULL) ORDER BY 1", "WHERE"},
				{"SELECT /*é*/ 1 WHERE num_nulls(NULL)", "WHERE"},
				{"SELECT 1 FROM bcp_empty WHERE num_nulls(id)", "WHERE"},
				{"SELECT TRUE AND num_nulls(NULL)", "AND"},
				{"SELECT TRUE OR num_nulls(NULL)", "OR"},
				{"SELECT NOT num_nulls(NULL)", "NOT"},
			} {
				pos := utf8.RuneCountInString(tc.q[:strings.Index(tc.q, "num_nulls")]) + 1
				check(tc.q, fixture.Diagnostic{Code: "42804", Message: "argument of " + tc.construct + " must be type boolean, not type integer", Position: strconv.Itoa(pos)})
			}
			for _, tc := range []struct{ q, construct string }{
				{"SELECT TRUE OR unnest(ARRAY[TRUE])", "OR"},
				{"SELECT TRUE AND unnest(ARRAY[TRUE])", "AND"},
				{"SELECT NOT unnest(ARRAY[TRUE])", "NOT"},
				{"SELECT TRUE OR ((unnest(ARRAY[TRUE])))", "OR"},
			} {
				check(tc.q, fixture.Diagnostic{Code: "42804", Message: "argument of " + tc.construct + " must not return a set", Position: strconv.Itoa(strings.Index(tc.q, "unnest") + 1)})
			}
			const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			for _, fn := range booleanCallFunctions {
				for _, prefix := range []string{"SELECT 1 WHERE ", "SELECT TRUE OR "} {
					for _, tc := range []struct{ call, signature, code, message string }{
						{fn + "()", fn + "()", "42883", ""},
						{fn + "(1,2)", fn + "(integer, integer)", "42883", ""},
						{fn + "(TRUE)", fn + "(boolean)", "42883", ""},
						{fn + "('1'::text)", fn + "(text)", "42883", ""},
						{"public." + fn + "(1)", "public." + fn + "(integer)", "42883", ""},
						{"missing_schema." + fn + "(1)", "", "3F000", `schema "missing_schema" does not exist`},
					} {
						d := fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: strconv.Itoa(len(prefix) + 1)}
						if tc.code == "42883" {
							d.Message = "function " + tc.signature + " does not exist"
							d.Hint = hint
						}
						check(prefix+tc.call, d)
					}
				}
				for _, tc := range []struct{ arg, code, message string }{
					{"'bad'", "22P02", `invalid input syntax for type oid: "bad"`},
					{"'4294967296'", "22003", `value "4294967296" is out of range for type oid`},
				} {
					q := "SELECT 1 WHERE " + fn + "(" + tc.arg + ")"
					check(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: strconv.Itoa(strings.Index(q, tc.arg) + 1)})
				}
				check("SELECT 1 WHERE "+fn+"($1)", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad")
				q := "SELECT FALSE AND missing_schema." + fn + "(1)"
				check(q, fixture.Diagnostic{Code: "3F000", Message: `schema "missing_schema" does not exist`, Position: "18"})
				q = "SELECT TRUE OR missing_schema." + fn + "(" + strings.Repeat("1,", 100) + "missing_column) FROM bcp_empty"
				check(q, fixture.Diagnostic{Code: "42703", Message: `column "missing_column" does not exist`, Position: strconv.Itoa(strings.Index(q, "missing_column") + 1)})
				q = "SELECT 1 WHERE " + fn + "(" + strings.TrimSuffix(strings.Repeat("1,", 101), ",") + ")"
				check(q, fixture.Diagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "16"})
				for _, tail := range []string{"(1,)", "(", "(1) AND", "(1) AND NOT"} {
					q := "SELECT 1 WHERE " + fn + tail
					d := fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: strconv.Itoa(len(q) + 1)}
					if tail == "(1,)" {
						d.Message = `syntax error at or near ")"`
						d.Position = strconv.Itoa(len(q))
					}
					check(q, d)
				}
				for _, name := range []string{fn + "_missing", `"` + strings.ToUpper(fn) + `"`} {
					q := "SELECT 1 WHERE " + name + "(1)"
					check(q, fixture.Diagnostic{Code: "42883", Message: "function " + strings.Trim(name, `"`) + "(integer) does not exist", Position: "16", Hint: hint})
				}
			}
			for _, tc := range []struct {
				q, code, message string
				pos              int
			}{
				{"SELECT 1 WHERE unnest(ARRAY[TRUE])", "0A000", "set-returning functions are not allowed in WHERE", 16},
				{"SELECT 1 WHERE any_value(TRUE)", "42803", "aggregate functions are not allowed in WHERE", 16},
				{"SELECT 1 WHERE row_number() OVER () = 1", "42P20", "window functions are not allowed in WHERE", 16},
				{"SELECT TRUE OR num_nulls(NULL) OVER ()", "42809", "OVER specified, but num_nulls is not a window function nor an aggregate function", 16},
			} {
				check(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: fixture.Position(tc.pos)})
			}
			q := "INSERT INTO bcp_sentinel VALUES(1); SELECT 1 WHERE pg_catalog.pg_table_is_visible(1) AND"
			check(q, fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: fmt.Sprint(len(q) + 1)})
			run(fixture.Select("SELECT count(*) FROM bcp_sentinel", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		})
	}
}
