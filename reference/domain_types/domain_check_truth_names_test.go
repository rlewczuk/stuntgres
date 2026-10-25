package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainCheckTruth(t *testing.T) {
	for _, tc := range []struct{ name, clause, constraint string }{
		{"named", "CONSTRAINT positive CHECK (VALUE>0)", "positive"},
		{"unnamed", "CHECK (VALUE>0)", "unnamed_check"},
		{"true_check", "CHECK (TRUE)", "true_check_check"},
		{"unknown_check", "CHECK (NULL::boolean)", "unknown_check_check"},
		{"false_check", "CHECK (FALSE)", "false_check_check"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				run(fixture.Exec("CREATE DOMAIN domain_s."+tc.name+" AS integer "+tc.clause, 0))
				for _, prepared := range []bool{false, true} {
					for _, value := range []string{"1", "0", "-1", "NULL"} {
						t.Run(value+map[bool]string{false: "_direct", true: "_prepared"}[prepared], func(t *testing.T) {
							q := "SELECT (" + value + ")::domain_s." + tc.name + " AS v FROM public.one"
							if tc.name == "false_check" || ((tc.name == "named" || tc.name == "unnamed") && (value == "0" || value == "-1")) {
								fixture.RunStep(t, p.Ctx, c, r, domainCheckFailure(q, tc.name, tc.constraint, true, prepared))
								return
							}
							var want any
							switch value {
							case "1":
								want = int64(1)
							case "0":
								want = int64(0)
							case "-1":
								want = int64(-1)
							}
							s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{want}})
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, c, r, s)
						})
					}
					if tc.name == "named" || tc.name == "unnamed" {
						t.Run("unary_precedence"+map[bool]string{false: "_direct", true: "_prepared"}[prepared], func(t *testing.T) {
							// Without parentheses, the positive cast occurs before
							// unary minus; the base operator does not recheck.
							s := fixture.Select("SELECT -1::domain_s."+tc.name+" AS v FROM public.one", []string{"v"}, []string{"INT4"}, [][]any{{int64(-1)}})
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, c, r, s)
						})
					}
					for _, value := range []any{int64(1), int64(0), int64(-1), nil} {
						t.Run("bind_"+map[bool]string{false: "direct", true: "prepared"}[prepared]+"_"+fmt.Sprint(value), func(t *testing.T) {
							q := "SELECT $1::domain_s." + tc.name + " AS v FROM public.one"
							if tc.name == "false_check" || ((tc.name == "named" || tc.name == "unnamed") && (value == int64(0) || value == int64(-1))) {
								s := domainCheckFailure(q, tc.name, tc.constraint, true, prepared, value)
								s.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
								if value == nil {
									s.Diagnostic.Context = "unnamed portal parameter $1"
								}
								fixture.RunStep(t, p.Ctx, c, r, s)
								return
							}
							s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{value}}, value)
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, c, r, s)
						})
					}
				}
			})
		})
	}
}

func TestReferenceDomainCheckNamesOrder(t *testing.T) {
	cases := []struct {
		name, setup, clause, constraint string
		duplicate                       bool
	}{
		{"ordered", "", "CONSTRAINT z_last CHECK (VALUE>0) CONSTRAINT a_first CHECK (VALUE>10)", "a_first", false},
		{"unnamed", "", "CHECK (VALUE>0) CHECK (VALUE>10)", "unnamed_check", false},
		{"explicit_first", "", "CONSTRAINT explicit_first_check CHECK (TRUE) CHECK (FALSE)", "explicit_first_check1", false},
		{"generated_first", "", "CHECK (TRUE) CONSTRAINT generated_first_check CHECK (FALSE)", "generated_first_check", true},
		{"table_collision", "CREATE TABLE domain_s.other (v integer CONSTRAINT table_collision_check CHECK (v>0))", "CHECK (FALSE)", "table_collision_check1", false},
		{"domain_collision", "CREATE DOMAIN domain_s.other AS integer CONSTRAINT domain_collision_check CHECK (TRUE)", "CHECK (FALSE)", "domain_collision_check1", false},
		{"duplicate", "", "CONSTRAINT same CHECK (TRUE) CONSTRAINT same CHECK (FALSE)", "same", true},
		{"shared", "CREATE DOMAIN domain_s.other AS integer CONSTRAINT same CHECK (TRUE)", "CONSTRAINT same CHECK (FALSE)", "same", false},
		{"quoted", "", "CONSTRAINT \"é first\" CHECK (FALSE) CONSTRAINT \"z last\" CHECK (FALSE)", "z last", false},
		{strings.Repeat("é", 31), "", "CHECK (FALSE)", strings.Repeat("é", 28) + "_check", false},
		{"suffix_order", "", "CONSTRAINT suffix_order_check2 CHECK (FALSE) CONSTRAINT suffix_order_check10 CHECK (FALSE)", "suffix_order_check10", false},
		{"generated_suffix", "", "CHECK (TRUE) CHECK (TRUE)" + strings.Repeat(" CHECK (FALSE)", 9), "generated_suffix_check10", false},
		{"duplicate_before_binding", "", "CONSTRAINT same CHECK (TRUE) CONSTRAINT same CHECK (missing>0)", "same", true},
		{"long_explicit", "", "CONSTRAINT \"" + strings.Repeat("é", 32) + "\" CHECK (FALSE)", strings.Repeat("é", 31), false},
		{strings.Repeat("x", 63), "", "CHECK (FALSE)", strings.Repeat("x", 57) + "_check", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				if tc.setup != "" {
					run(fixture.Exec(tc.setup, 0))
				}
				q := "CREATE DOMAIN domain_s." + tc.name + " AS integer " + tc.clause
				if tc.duplicate {
					want := fixture.ControlsReferenceDiagnostic("42710", "constraint \""+tc.constraint+"\" for domain \""+tc.name+"\" already exists")
					run(fixture.ErrorDiagnostic(q, want))
					run(fixture.Exec("CREATE DOMAIN domain_s."+tc.name+" AS integer CHECK (TRUE)", 0))
					return
				}
				run(fixture.Exec(q, 0))
				for _, prepared := range []bool{false, true} {
					t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
						fixture.RunStep(t, p.Ctx, c, r, domainCheckFailure("SELECT 0::domain_s."+tc.name+" AS v FROM public.one", tc.name, tc.constraint, true, prepared))
					})
				}
			})
		})
	}
}
