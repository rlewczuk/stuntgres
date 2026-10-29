package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVisibilityInquiryDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db, rec := backend.DB, backend.Recorder
			const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			for _, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible", "pg_ts_config_is_visible", "pg_ts_dict_is_visible", "pg_ts_parser_is_visible", "pg_ts_template_is_visible", "pg_statistics_obj_is_visible"} {
				cases := []struct {
					q    string
					want fixture.Diagnostic
				}{
					{"SELECT " + fn + "()", fixture.Diagnostic{Code: "42883", Message: "function " + fn + "() does not exist", Position: "8", Hint: hint}},
					{"SELECT " + fn + "(1,2)", fixture.Diagnostic{Code: "42883", Message: "function " + fn + "(integer, integer) does not exist", Position: "8", Hint: hint}},
					{"SELECT " + fn + "(TRUE)", fixture.Diagnostic{Code: "42883", Message: "function " + fn + "(boolean) does not exist", Position: "8", Hint: hint}},
					{"SELECT " + fn + "('1'::text)", fixture.Diagnostic{Code: "42883", Message: "function " + fn + "(text) does not exist", Position: "8", Hint: hint}},
					{"SELECT public." + fn + "(1)", fixture.Diagnostic{Code: "42883", Message: "function public." + fn + "(integer) does not exist", Position: "8", Hint: hint}},
					{"SELECT missing_schema." + fn + "(1)", fixture.Diagnostic{Code: "3F000", Message: `schema "missing_schema" does not exist`, Position: "8"}},
					{"SELECT /*é*/ " + fn + "(TRUE)", fixture.Diagnostic{Code: "42883", Message: "function " + fn + "(boolean) does not exist", Position: "14", Hint: hint}},
					{"SELECT " + fn + "('bad')", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Position: strconv.Itoa(9 + len(fn))}},
					{"SELECT " + fn + "('4294967296')", fixture.Diagnostic{Code: "22003", Message: `value "4294967296" is out of range for type oid`, Position: strconv.Itoa(9 + len(fn))}},
					{"SELECT " + fn[:len(fn)-1] + "(0::oid)", fixture.Diagnostic{Code: "42883", Message: "function " + fn[:len(fn)-1] + "(oid) does not exist", Position: "8", Hint: hint}},
				}
				for _, tc := range cases {
					for _, source := range []bool{false, true} {
						s := fixture.ImplicitError(tc.q, tc.want)
						s.SelectRows = true
						s.NonContext = source
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, db, rec, s)
						s.ErrorStage = "prepare"
						fixture.RunStep(t, p.Ctx, db, rec, fixture.Prepared(s))
					}
				}
				s := fixture.ImplicitError("SELECT "+fn+"($1)", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad")
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, db, rec, fixture.Prepared(s))
				s.NonContext = true
				fixture.RunStep(t, p.Ctx, db, rec, fixture.Prepared(s))
				q := "SELECT " + fn + "($1)"
				held, err := db.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				executor := fixture.HeldStatement{Executor: db, Statement: held, SQL: q}
				s.NonContext = false
				fixture.RunStep(t, p.Ctx, executor, rec, s)
				fixture.RunStep(t, p.Ctx, executor, rec, fixture.Select(q, []string{fn}, []string{"BOOL"}, [][]any{{nil}}, nil))
				if err := held.Close(); err != nil {
					t.Fatal(err)
				}
				matched := fixture.Select("SELECT "+fn+"(VARIADIC NULL)", []string{fn}, []string{"BOOL"}, [][]any{{nil}})
				fixture.RunStep(t, p.Ctx, db, rec, matched)
				fixture.RunStep(t, p.Ctx, db, rec, fixture.Prepared(matched))
				matched.NonContext = true
				matched.CheckMetadata = fixture.VisibilityBoolMetadata
				fixture.RunStep(t, p.Ctx, db, rec, matched)
				args := strings.TrimSuffix(strings.Repeat("1,", 101), ",")
				limit := fixture.ImplicitError("SELECT "+fn+"("+args+")", fixture.Diagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"})
				limit.SelectRows = true
				limit.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, db, rec, limit)
				limit.NonContext = true
				fixture.RunStep(t, p.Ctx, db, rec, limit)
				limit.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, db, rec, fixture.Prepared(limit))
			}
		})
	}
}

func TestReferenceVisibilityInquiryDiagnosticOrdering(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(strconv.FormatBool(source), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					db, rec := backend.DB, backend.Recorder
					setup := fixture.Exec("CREATE TABLE empty_ids(id OID)", 0)
					setup.NonContext = source
					fixture.RunStep(t, p.Ctx, db, rec, setup)
					for _, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible", "pg_ts_config_is_visible", "pg_ts_dict_is_visible", "pg_ts_parser_is_visible", "pg_ts_template_is_visible", "pg_statistics_obj_is_visible"} {
						t.Run(fn, func(t *testing.T) {
							for _, tc := range []struct {
								name string
								args string
								want fixture.Diagnostic
							}{
								{"missing_child", "missing_column", fixture.Diagnostic{Code: "42703", Message: `column "missing_column" does not exist`}},
								{"argument_limit", strings.TrimSuffix(strings.Repeat("1,", 101), ","), fixture.Diagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}},
								{"over_limit_missing_child", strings.Repeat("1,", 100) + "missing_column", fixture.Diagnostic{Code: "42703", Message: `column "missing_column" does not exist`}},
							} {
								t.Run(tc.name, func(t *testing.T) {
									q := "SELECT missing_schema." + fn + "(" + tc.args + ") FROM empty_ids"
									if tc.want.Code == "42703" {
										tc.want.Position = strconv.Itoa(strings.Index(q, "missing_column") + 1)
									}
									s := fixture.ImplicitError(q, tc.want)
									s.SelectRows = true
									s.NonContext = source
									s.ErrorStage = "execute"
									fixture.RunStep(t, p.Ctx, db, rec, s)
									s.ErrorStage = "prepare"
									fixture.RunStep(t, p.Ctx, db, rec, fixture.Prepared(s))
								})
							}
						})
					}
				})
			}
		})
	}
}
