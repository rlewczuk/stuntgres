package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// The zero values in Diagnostic are intentional: every logical optional field
// must be absent unless explicitly independently specified here.
func selectedCallErrorBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, d fixture.Diagnostic, runtime bool, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("%s/prepared=%v", q, prepared), func(t *testing.T) {
			s := fixture.ImplicitError(q, d, args...)
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared && !runtime {
				s.ErrorStage = "prepare"
			}
			if runtime {
				selectedCallRuntimeError(t, p, b, c, q, d, prepared, args...)
			} else {
				selectedCallRun(t, p, b, c, s)
			}
			good := fixture.Select("SELECT 1+abs(-1) AS recovered", []string{"recovered"}, []string{"INT4"}, [][]any{{int64(2)}})
			good.Prepared = prepared
			selectedCallRun(t, p, b, c, good)
		})
	}
}

func selectedCallPosition(q, token string) string {
	return strconv.Itoa(utf8.RuneCountInString(q[:strings.LastIndex(q, token)]) + 1)
}

func TestReferenceSelectedCallOperandsDiagnostics(t *testing.T) {
	selectedCallScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct {
			q, code, message, position, hint string
			runtime                          bool
		}{
			{"SELECT 1 + abs('x')", "22P02", `invalid input syntax for type double precision: "x"`, "16", "", false},
			{"SELECT /*é*/ 1 + abs('x')", "22P02", `invalid input syntax for type double precision: "x"`, "22", "", false},
			{"SELECT abs('x') + 1", "22P02", `invalid input syntax for type double precision: "x"`, "12", "", false},
			{"SELECT 1 + mod(1,0)", "22012", "division by zero", "", "", true},
			{"SELECT mod(1,0) + 1", "22012", "division by zero", "", "", true},
			{"SELECT 1 + factorial(-1)", "22003", "factorial of a negative number is undefined", "", "", true},
			{"SELECT factorial(-1) + 1", "22003", "factorial of a negative number is undefined", "", "", true},
			{"SELECT abs() + 1", "42883", "function abs() does not exist", "8", hint, false},
			{"SELECT 1 + pg_catalog.abs()", "42883", "function pg_catalog.abs() does not exist", "12", hint, false},
			{"SELECT 1 + public.abs(-1)", "42883", "function public.abs(integer) does not exist", "12", hint, false},
			{`SELECT 1 + "ABS"(-1)`, "42883", "function ABS(integer) does not exist", "12", hint, false},
			{"SELECT (abs(-1)).missing", "42809", "column notation .missing applied to type integer, which is not a composite type", "9", "", false},
			{"SELECT (pi() FILTER (WHERE true))::text", "42809", "FILTER specified, but pi is not an aggregate function", "9", "", false},
			{"SELECT 1+abs(1) OVER ()", "42809", "OVER specified, but abs is not a window function nor an aggregate function", "10", "", false},
		} {
			selectedCallErrorBoth(t, p, b, c, tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position, Hint: tc.hint}, tc.runtime)
		}
		for _, q := range []string{"SELECT 1+abs($1)", "SELECT abs($1)+1", "SELECT CAST(abs($1) AS text)"} {
			selectedCallErrorBoth(t, p, b, c, q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type double precision: "x"`, Context: "unnamed portal parameter $1 = '...'"}, true, "x")
		}
		selectedCallErrorBoth(t, p, b, c, "SELECT 1+mod($1::int4,$2::int4)", fixture.Diagnostic{Code: "22012", Message: "division by zero"}, true, int64(1), int64(0))
		selectedCallErrorBoth(t, p, b, c, "SELECT 1+factorial($1)", fixture.Diagnostic{Code: "22003", Message: "factorial of a negative number is undefined"}, true, int64(-1))
		for _, tc := range []struct {
			q, name, typ string
			want         any
		}{
			{"SELECT abs(NULL::int4)+1", "?column?", "INT4", nil},
			{"SELECT 1+abs(NULL::int4)", "?column?", "INT4", nil},
			{"SELECT abs(NULL::int4)::text", "abs", "TEXT", nil},
			{"SELECT 1+abs('-2')", "?column?", "FLOAT8", float64(3)},
		} {
			selectedCallBoth(t, p, b, c, fixture.Select(tc.q, []string{tc.name}, []string{tc.typ}, [][]any{{tc.want}}))
		}
		selectedCallRun(t, p, b, c, fixture.Exec("CREATE TABLE selected_script(v integer)", 0))
		selectedCallRun(t, p, b, c, fixture.Exec("INSERT INTO selected_script VALUES(7)", 1))
		for _, q := range []string{
			"SELECT /*é*/ 1+abs(-1) +",
			"SELECT /*é*/ 1+abs(-1); SELECT abs(1,)",
			"INSERT INTO selected_script VALUES(abs(-1)); SELECT /*é*/ 1+abs(1,)",
		} {
			message := "syntax error at or near \")\""
			position := strconv.Itoa(utf8.RuneCountInString(q))
			if strings.HasSuffix(q, "+") {
				message = "syntax error at end of input"
				position = strconv.Itoa(utf8.RuneCountInString(q) + 1)
			}
			selectedCallErrorBoth(t, p, b, c, q, fixture.Diagnostic{Code: "42601", Message: message, Position: position}, false)
			selectedCallRun(t, p, b, c, fixture.Select("SELECT v FROM selected_script", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
	})
}

func TestReferenceSelectedCallOperandsBoundaries(t *testing.T) {
	selectedCallScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for i, tc := range []struct {
			expression, typ, function string
			want                      any
			direct                    bool
		}{
			{"abs(-2)", "integer", "abs", int64(2), true},
			{"(abs(-2))", "integer", "abs", int64(2), false},
			{"abs(-2)::text", "text", "abs", "2", false},
			{"1+abs(-1)", "integer", "abs", int64(2), false},
			{"CAST(abs(-2) AS text)", "text", "abs", "2", false},
			{"(trunc(1.9))", "numeric", "trunc", "1", false},
		} {
			for _, prepared := range []bool{false, true} {
				name := fmt.Sprintf("selected_default_%d_%v", i, prepared)
				q := "CREATE TABLE " + name + "(v " + tc.typ + " DEFAULT " + tc.expression + ")"
				if b.PG {
					s := fixture.Exec(q, 0)
					s.Prepared = prepared
					selectedCallRun(t, p, b, c, s)
					s = fixture.Exec("INSERT INTO "+name+" DEFAULT VALUES", 1)
					s.Prepared = prepared
					selectedCallRun(t, p, b, c, s)
					typ := "INT4"
					if tc.typ == "text" {
						typ = "TEXT"
					} else if tc.typ == "numeric" {
						typ = "NUMERIC"
					}
					selectedCallRun(t, p, b, c, fixture.Select("SELECT v FROM "+name, []string{"v"}, []string{typ}, [][]any{{tc.want}}))
				} else {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "functions are unsupported", Position: selectedCallPosition(q, tc.function)})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared && tc.direct {
						s.ErrorStage = "prepare"
					}
					selectedCallRun(t, p, b, c, s)
					selectedCallRun(t, p, b, c, fixture.Select("SELECT count(*) FROM pg_class WHERE relname=$1 AND relnamespace='public'::regnamespace", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, name))
				}
			}
		}
		// Existing logarithm/root and typed MAC trunc DEFAULT owners remain
		// usable. Numeric trunc retains its separate unsupported boundary.
		selectedCallRun(t, p, b, c, fixture.Exec("CREATE TABLE selected_supported_defaults(a float8 DEFAULT ln(1::float8),b float8 DEFAULT sqrt(4::float8),c macaddr DEFAULT (trunc('08:00:2b:01:02:03'::macaddr)))", 0))
		selectedCallRun(t, p, b, c, fixture.Exec("INSERT INTO selected_supported_defaults DEFAULT VALUES", 1))
		selectedCallBoth(t, p, b, c, fixture.Select("SELECT a,b,c FROM selected_supported_defaults", []string{"a", "b", "c"}, []string{"FLOAT8", "FLOAT8", "MACADDR"}, [][]any{{float64(0), float64(2), "08:00:2b:00:00:00"}}))
		for _, prepared := range []bool{false, true} {
			for _, expression := range []string{"random()+0::float8", "CAST(random() AS float8)"} {
				q := "CREATE TABLE selected_bad_generated(v float8 GENERATED ALWAYS AS (" + expression + ") STORED)"
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P17", Message: "generation expression is not immutable"})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				selectedCallRun(t, p, b, c, s)
				selectedCallRun(t, p, b, c, fixture.Select("SELECT count(*) FROM pg_class WHERE relname='selected_bad_generated' AND relnamespace='public'::regnamespace", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
		}
		selectedCallRun(t, p, b, c, fixture.Exec("CREATE TABLE selected_generated(a integer,b integer GENERATED ALWAYS AS (1+abs(a)) STORED)", 0))
		selectedCallRun(t, p, b, c, fixture.Exec("INSERT INTO selected_generated(a) VALUES(-2),(NULL)", 2))
		selectedCallBoth(t, p, b, c, fixture.Select("SELECT a,b FROM selected_generated ORDER BY a NULLS LAST", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(-2), int64(3)}, {nil, nil}}))
	})
}
