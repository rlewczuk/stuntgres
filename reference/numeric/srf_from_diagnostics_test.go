package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSRFFromDiagnostics(t *testing.T) {
	const noMatch = "No function matches the given name and argument types. You might need to add explicit type casts."
	const ambiguous = "Could not choose a best candidate function. You might need to add explicit type casts."
	// Independent expectations corrected against pinned PostgreSQL 18.0 discovery.
	// Column definitions locate the first alias name, not its declared type.
	cases := []struct{ q, token, code, message, detail, hint string }{
		{"SELECT * FROM generate_series(1,generate_series(1,2))", "generate_series(1,2)", "0A000", "set-returning functions must appear at top level of FROM", "", ""},
		{"SELECT * FROM abs(generate_series(1,2))", "generate_series", "0A000", "set-returning functions must appear at top level of FROM", "", ""},
		{"SELECT * FROM generate_series(true,false)", "generate_series", "42883", "function generate_series(boolean, boolean) does not exist", "", noMatch},
		{"SELECT * FROM generate_series(NULL,NULL)", "generate_series", "42725", "function generate_series(unknown, unknown) is not unique", "", ambiguous},
		{"SELECT * FROM generate_series(1,s.n) g(v),(VALUES(2)) s(n)", "s.n", "42P01", `missing FROM-clause entry for table "s"`, "", ""},
		{"SELECT * FROM (VALUES(2)) s(n) RIGHT JOIN LATERAL generate_series(1,s.n) g(v) ON true", "s.n", "42P10", `invalid reference to FROM-clause entry for table "s"`, "The combining JOIN type must be INNER or LEFT for a LATERAL reference.", ""},
		{"SELECT * FROM (VALUES(2)) s(n) FULL JOIN LATERAL generate_series(1,s.n) g(v) ON true", "s.n", "42P10", `invalid reference to FROM-clause entry for table "s"`, "The combining JOIN type must be INNER or LEFT for a LATERAL reference.", ""},
		{"SELECT v FROM generate_series(1,2) a(v),generate_series(3,4) b(v)", "v FROM", "42702", `column reference "v" is ambiguous`, "", ""},
		{"SELECT * FROM generate_series(1,2) AS g(v,n)", "", "42P10", `table "g" has 1 columns available but 2 columns specified`, "", ""},
		{"SELECT * FROM generate_series(1,2) AS g(v integer)", "v integer", "42601", `a column definition list is only allowed for functions returning "record"`, "", ""},
		{"SELECT * FROM generate_series(1,2) WITH nope", "WITH", "42601", `syntax error at or near "WITH"`, "", ""},
		{"SELECT * FROM ROWS FROM(generate_series(1,2),)", ")", "42601", `syntax error at or near ")"`, "", ""},
		{"SELECT * FROM generate_series(1,2) WITH ORDINALITY WITH ORDINALITY", "WITH ORDINALITY", "42601", `syntax error at or near "WITH"`, "", ""},
		{"SELECT generate_series.generate_series FROM generate_series(1,2) AS g", "generate_series.generate_series", "42P01", `missing FROM-clause entry for table "generate_series"`, "", ""},
		{"SELECT g.missing FROM generate_series(1,2) AS g(v)", "g.missing", "42703", "column g.missing does not exist", "", ""},
		{"SELECT * FROM generate_series(1,2) g,generate_series(3,4) g", "", "42712", `table name "g" specified more than once`, "", ""},
		{"SELECT * FROM ROWS FROM(generate_series(1,2),generate_series(1,generate_series.generate_series))", "generate_series.generate_series", "42P01", `missing FROM-clause entry for table "generate_series"`, "", ""},
		{"SELECT * FROM ROWS FROM(generate_series(1,2),generate_series(3,4)) AS r(a integer)", "a integer", "42601", "ROWS FROM() with multiple functions cannot have a column definition list", "", "Put a separate column definition list for each function inside ROWS FROM()."},
		{"SELECT * FROM generate_series(1,2) WITH ORDINALITY AS g(v integer)", "v integer", "42601", "WITH ORDINALITY cannot be used with a column definition list", "", "Put the column definition list inside ROWS FROM()."},
		{"SELECT * FROM generate_series(1,2); trailing", "trailing", "42601", `syntax error at or near "trailing"`, "", ""},
		// REL_18_0 parse_clause.c transforms calls before top-level coldefs.
		{"SELECT * FROM generate_series(true,false) AS g(v integer)", "generate_series", "42883", "function generate_series(boolean, boolean) does not exist", "", noMatch},
		{"SELECT * FROM generate_series(true,false) WITH ORDINALITY AS g(v integer)", "generate_series", "42883", "function generate_series(boolean, boolean) does not exist", "", noMatch},
		{"SELECT * FROM ROWS FROM(generate_series(1,2),generate_series(true,false)) WITH ORDINALITY AS r(v integer)", "generate_series(true,false)", "42883", "function generate_series(boolean, boolean) does not exist", "", noMatch},
		{"SELECT * FROM generate_series(1,generate_series(1,2)) AS g(v integer)", "generate_series(1,2)", "0A000", "set-returning functions must appear at top level of FROM", "", ""},
		{"SELECT * FROM generate_series(1,generate_series(1,2)) WITH ORDINALITY AS g(v integer)", "generate_series(1,2)", "0A000", "set-returning functions must appear at top level of FROM", "", ""},
		{"SELECT * FROM ROWS FROM(generate_series(1,2),generate_series(1,generate_series(3,4))) AS r(v integer)", "generate_series(3,4)", "0A000", "set-returning functions must appear at top level of FROM", "", ""},
		{"SELECT * FROM ROWS FROM(generate_series(1,2),generate_series(3,4)) WITH ORDINALITY AS r(v integer)", "v integer", "42601", "ROWS FROM() with multiple functions cannot have a column definition list", "", "Put a separate column definition list for each function inside ROWS FROM()."},
		// REL_18_0 parse_expr.c visits arguments in written order; parse_func.c
		// records the last completed SRF, but earlier nested analysis can fail.
		{"SELECT * FROM generate_series(generate_series(1,2),generate_series(3,4))", "generate_series(3,4)", "0A000", "set-returning functions must appear at top level of FROM", "", ""},
		{"SELECT * FROM generate_series(abs(generate_series(1,2)),abs(generate_series(3,4)))", "generate_series(3,4)", "0A000", "set-returning functions must appear at top level of FROM", "", ""},
		{"SELECT * FROM generate_series(generate_series(1,generate_series(2,3)),generate_series(4,5))", "generate_series(2,3)", "0A000", "set-returning functions must appear at top level of FROM", "", ""},
		{"SELECT * FROM regexp_split_to_table(regexp_split_to_table('a,b',','),regexp_split_to_table('c,d',','))", "regexp_split_to_table('c,d',',')", "0A000", "set-returning functions must appear at top level of FROM", "", ""},
		{"SELECT * FROM ROWS FROM(generate_series(1,2),regexp_split_to_table(regexp_split_to_table('a,b',','),string_to_table('c,d',',')))", "string_to_table", "0A000", "set-returning functions must appear at top level of FROM", "", ""},
		// A child SELECT owns its SRFs; it cannot replace the lexical offender.
		{"SELECT * FROM generate_series(generate_series(1,2),(SELECT generate_series(3,3)))", "generate_series(1,2)", "0A000", "set-returning functions must appear at top level of FROM", "", ""},
		{"SELECT * FROM generate_series((SELECT generate_series(1,1)),generate_series(3,4))", "generate_series(3,4)", "0A000", "set-returning functions must appear at top level of FROM", "", ""},
		{"SELECT * FROM generate_series(generate_series(1,2)+1,generate_series(3,4)+1)", "generate_series(3,4)", "0A000", "set-returning functions must appear at top level of FROM", "", ""},
		{"SELECT * FROM generate_series(1,(SELECT generate_series(2,2))) AS g(v integer)", "v integer", "42601", `a column definition list is only allowed for functions returning "record"`, "", ""},
	}
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range cases {
			t.Run(tc.q, func(t *testing.T) {
				for _, prefix := range []string{"", "/*é🙂*/ "} {
					q := prefix + tc.q
					d := seriesDiagnostic(q, tc.token, tc.code, tc.message, tc.hint)
					d.Detail = tc.detail
					for _, prepared := range []bool{false, true} {
						t.Run(prefix+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
							s := fixture.ErrorDiagnostic(q, d)
							s.Prepared, s.SelectRows = prepared, true
							s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						})
					}
				}
			})
		}
		for _, q := range []string{"SELECT * FROM generate_series(1,3,0)", "SELECT * FROM generate_series(1,(SELECT generate_series(1,2)))"} {
			code, message := "22023", "step size cannot equal zero"
			if q != "SELECT * FROM generate_series(1,3,0)" {
				code, message = "21000", "more than one row returned by a subquery used as an expression"
			}
			s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", code, message, ""))
			s.SelectRows, s.ErrorStage = true, "execute"
			seriesBoth(t, p, b, c, s)
		}
		// A syntax error after a valid prefix must not execute that prefix.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE srf_from_suffix(v integer)", 0))
		const q = "INSERT INTO srf_from_suffix SELECT * FROM generate_series(1,2); trailing"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "trailing", "42601", `syntax error at or near "trailing"`, ""))
			s.Prepared = prepared
			s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM srf_from_suffix", []string{"v"}, []string{"INT4"}, nil))
	})
}
