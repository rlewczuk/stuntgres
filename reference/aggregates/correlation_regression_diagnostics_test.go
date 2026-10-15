package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func correlationBindingError(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, q string, d fixture.Diagnostic, generated bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		for _, query := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, d)
			s.Prepared, s.SelectRows, s.ErrorStage = prepared, query, "execute"
			if prepared && !generated {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, r, s)
		}
	}
}

func TestReferenceCorrelationRegressionDiagnostics(t *testing.T) {
	correlationScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE TABLE t(i INTEGER,n NUMERIC)", 0))
		run(fixture.Exec("INSERT INTO t VALUES (1,1e309)", 1))
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for i, name := range correlationNames {
			for _, arg := range []struct{ sql, typ string }{
				{"TRUE,i", "boolean, integer"}, {"i,TRUE", "integer, boolean"},
				{"'x'::text,i", "text, integer"}, {"i", "integer"}, {"", ""}, {"*", ""}, {"1,2,3", "integer, integer, integer"},
			} {
				q := "SELECT " + name + "(" + arg.sql + ") FROM t WHERE FALSE"
				d := fixture.ControlsReferenceDiagnostic("42883", "function "+name+"("+arg.typ+") does not exist")
				d.Position, d.Hint = "8", hint
				correlationBindingError(t, p, c, r, q, d, false)
			}
			for _, prefix := range []string{"SELECT /*é*/ ", "SELECT pg_catalog."} {
				q := prefix + name + "(TRUE,i) FROM t"
				callee, position := name, "14"
				if strings.Contains(prefix, "pg_catalog") {
					callee, position = "pg_catalog."+name, "8"
				}
				d := fixture.ControlsReferenceDiagnostic("42883", "function "+callee+"(boolean, integer) does not exist")
				d.Position, d.Hint = position, hint
				correlationBindingError(t, p, c, r, q, d, false)
			}
			for _, tc := range []struct{ q, code, message, marker, hint string }{
				{"SELECT missing_stats." + name + "(i,i) FROM t", "3F000", `schema "missing_stats" does not exist`, "missing_stats", ""},
				{"SELECT public." + name + "(i,i) FROM t", "42883", "function public." + name + "(integer, integer) does not exist", "public", hint},
				{"SELECT " + name + "(i,i) FILTER (WHERE 1) FROM t", "42804", "argument of FILTER must be type boolean, not type integer", "1)", ""},
				{"SELECT i FROM t WHERE " + name + "(i,i)=0", "42803", "aggregate functions are not allowed in WHERE", name, ""},
				{"SELECT " + name + "(i,corr(i,i)) FROM t", "42803", "aggregate function calls cannot be nested", "corr(i,i)", ""},
			} {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Position = strconv.Itoa(utf8.RuneCountInString(tc.q[:strings.Index(tc.q, tc.marker)]) + 1)
				d.Hint = tc.hint
				correlationBindingError(t, p, c, r, tc.q, d, false)
			}
			for _, callee := range []string{name, "\"" + name + "\"", "pg_catalog.\"" + name + "\""} {
				q := "CREATE TABLE bad_correlation(i INTEGER,g DOUBLE PRECISION GENERATED ALWAYS AS (" + callee + "(i,i)) STORED)"
				d := fixture.ControlsReferenceDiagnostic("42803", "aggregate functions are not allowed in column generation expressions")
				d.Position = strconv.Itoa(strings.LastIndex(q, callee) + 1)
				correlationBindingError(t, p, c, r, q, d, true)
			}
			q := "SELECT pg_catalog.\"" + name + "\"(3*i,i) OVER () FROM t"
			if pg {
				want := []any{nil, float64(0), nil, float64(1), float64(3), int64(1), nil, nil}
				statisticalBoth(t, p, c, r, fixture.Select(q, []string{name}, []string{correlationTypes[i]}, [][]any{{want[i]}}))
			} else {
				d := fixture.ControlsReferenceDiagnostic("0A000", "window function provider is unsupported")
				d.Position = "8"
				correlationBindingError(t, p, c, r, q, d, false)
			}
		}
		// Shared argument-demand semantics need one floating and one exact-count
		// witness, not an eight-name Cartesian product of identical execution paths.
		for _, name := range []string{"corr", "regr_count"} {
			for _, args := range []string{"NULL::double precision,1/(i-1)", "1/(i-1),NULL::double precision"} {
				base := "SELECT " + name + "(" + args + ")"
				s := fixture.ErrorDiagnostic(base+" FROM t", fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
				s.SelectRows, s.ErrorStage = true, "execute"
				statisticalBoth(t, p, c, r, s)
				var value any
				typ := "FLOAT8"
				if name == "regr_count" {
					value, typ = int64(0), "INT8"
				}
				for _, filter := range []string{"FALSE", "NULL::boolean", "i>1"} {
					statisticalBoth(t, p, c, r, fixture.Select(base+" FILTER (WHERE "+filter+") FROM t", []string{name}, []string{typ}, [][]any{{value}}))
				}
			}
			for _, args := range []string{"NULL::double precision,n", "n,NULL::double precision"} {
				q := "SELECT " + name + "(" + args + ") FROM t"
				d := fixture.ControlsReferenceDiagnostic("22003", "\"1"+strings.Repeat("0", 309)+"\" is out of range for type double precision")
				s := fixture.ErrorDiagnostic(q, d)
				s.SelectRows, s.ErrorStage = true, "execute"
				statisticalBoth(t, p, c, r, s)
			}
		}
		q := "SELECT regr_count(i,i) FILTER (WHERE 1/(i-1)>0) FROM t"
		s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
		s.SelectRows, s.ErrorStage = true, "execute"
		statisticalBoth(t, p, c, r, s)
	})
}
