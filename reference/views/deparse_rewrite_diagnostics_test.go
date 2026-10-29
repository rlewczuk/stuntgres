package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceDeparseRewriteDiagnostics(t *testing.T) {
	for _, f := range []string{"pg_get_triggerdef", "pg_get_ruledef"} {
		t.Run(f, func(t *testing.T) {
			fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				rewriteSeed(t, p, b, c)
				type diagnosticCase struct {
					q       string
					want    fixture.Diagnostic
					execute bool
					args    []any
				}
				var cases []diagnosticCase
				for _, tc := range []struct{ call, sig string }{
					{f + "()", f + "()"}, {f + "(0::oid,false,true)", f + "(oid, boolean, boolean)"},
					{f + "(true)", f + "(boolean)"}, {f + "('x'::text)", f + "(text)"}, {f + "(0::oid,1)", f + "(oid, integer)"},
					{`"` + strings.ToUpper(f) + `"(0::oid)`, strings.ToUpper(f) + "(oid)"}, {"public." + f + "(0::oid)", "public." + f + "(oid)"},
				} {
					w := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.sig+" does not exist")
					w.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					w.Position = "8"
					cases = append(cases, diagnosticCase{q: "SELECT " + tc.call, want: w})
				}
				for _, tc := range []struct{ call, code, message, token string }{
					{f + "('bad')", "22P02", `invalid input syntax for type oid: "bad"`, "'bad'"},
					{f + "('4294967296')", "22003", `value "4294967296" is out of range for type oid`, "'4294967296'"},
					{f + "(NULL::oid,'bad')", "22P02", `invalid input syntax for type boolean: "bad"`, "'bad'"},
					{f + "(NULL::oid,missing) FROM t", "42703", `column "missing" does not exist`, "missing"},
					{"absent_schema." + f + "(0::oid)", "3F000", `schema "absent_schema" does not exist`, "absent_schema"},
				} {
					q := "SELECT " + tc.call
					w := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					w.Position = fixture.Position(strings.Index(q, tc.token) + 1)
					cases = append(cases, diagnosticCase{q: q, want: w})
				}
				q := "SELECT 'été', " + f + "() FROM t"
				w := fixture.ControlsReferenceDiagnostic("42883", "function "+f+"() does not exist")
				w.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				w.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, f)]) + 1)
				cases = append(cases, diagnosticCase{q: q, want: w})
				for _, tc := range []struct{ value, code, message string }{
					{"bad", "22P02", `invalid input syntax for type oid: "bad"`},
					{"4294967296", "22003", `value "4294967296" is out of range for type oid`},
				} {
					w := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					w.Context = "unnamed portal parameter $1 = '...'"
					cases = append(cases, diagnosticCase{q: "SELECT " + f + "($1)", want: w, execute: true, args: []any{tc.value}})
				}
				w = fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "bad"`)
				w.Context = "unnamed portal parameter $2 = '...'"
				cases = append(cases, diagnosticCase{q: "SELECT " + f + "($1,$2)", want: w, execute: true, args: []any{int64(0), "bad"}})
				for _, tc := range cases {
					for _, prepared := range []bool{false, true} {
						t.Run(tc.q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
							var err error
							stage := "execute"
							if prepared {
								var stmt *sql.Stmt
								stmt, err = c.PrepareContext(p.Ctx, tc.q)
								if err != nil {
									stage = "prepare"
								} else {
									defer stmt.Close()
									var rows *sql.Rows
									rows, err = stmt.QueryContext(p.Ctx, tc.args...)
									if rows != nil {
										rows.Close()
										t.Fatal("rejected call published rows")
									}
								}
							} else {
								var rows *sql.Rows
								rows, err = c.QueryContext(p.Ctx, tc.q, tc.args...)
								if rows != nil {
									rows.Close()
									t.Fatal("rejected call published rows")
								}
							}
							if err == nil {
								t.Fatal("missing diagnostic")
							}
							if prepared && (stage == "execute") != tc.execute {
								t.Errorf("stage %s, want execute=%v", stage, tc.execute)
							}
							fixture.AssertDiagnostic(t, err, &tc.want)
						})
					}
				}
				// Analysis and failed parameter conversion cannot publish DML/catalog effects.
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				rewriteText(t, p, b, c, "SELECT pg_get_triggerdef(oid) AS definition FROM pg_trigger WHERE tgrelid='t'::regclass AND tgname='tr'", rewriteTriggerDefault)
				rewriteText(t, p, b, c, "SELECT pg_get_ruledef(oid) AS definition FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='r'", rewriteRuleDefault)
			})
		})
	}
}
