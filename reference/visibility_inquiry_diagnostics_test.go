//go:build reference

package reference_test

import (
	"strconv"
	"strings"
	"testing"
)

func TestReferenceVisibilityInquiryDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []string{"Oracle", "Native"} {
		t.Run(backend, func(t *testing.T) {
			db, rec := p.postgres, p.postgresRecord
			if backend == "Native" {
				db, rec = p.memory, p.memoryRecord
			}
			const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			for _, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
				cases := []struct {
					q    string
					want referenceDiagnostic
				}{
					{"SELECT " + fn + "()", referenceDiagnostic{Code: "42883", Message: "function " + fn + "() does not exist", Position: "8", Hint: hint}},
					{"SELECT " + fn + "(1,2)", referenceDiagnostic{Code: "42883", Message: "function " + fn + "(integer, integer) does not exist", Position: "8", Hint: hint}},
					{"SELECT " + fn + "(TRUE)", referenceDiagnostic{Code: "42883", Message: "function " + fn + "(boolean) does not exist", Position: "8", Hint: hint}},
					{"SELECT " + fn + "('1'::text)", referenceDiagnostic{Code: "42883", Message: "function " + fn + "(text) does not exist", Position: "8", Hint: hint}},
					{"SELECT public." + fn + "(1)", referenceDiagnostic{Code: "42883", Message: "function public." + fn + "(integer) does not exist", Position: "8", Hint: hint}},
					{"SELECT missing_schema." + fn + "(1)", referenceDiagnostic{Code: "3F000", Message: `schema "missing_schema" does not exist`, Position: "8"}},
					{"SELECT /*é*/ " + fn + "(TRUE)", referenceDiagnostic{Code: "42883", Message: "function " + fn + "(boolean) does not exist", Position: "14", Hint: hint}},
					{"SELECT " + fn + "('bad')", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Position: strconv.Itoa(9 + len(fn))}},
					{"SELECT " + fn + "('4294967296')", referenceDiagnostic{Code: "22003", Message: `value "4294967296" is out of range for type oid`, Position: strconv.Itoa(9 + len(fn))}},
				}
				for _, tc := range cases {
					s := referenceImplicitError(tc.q, tc.want)
					s.selectRows = true
					s.errorStage = "execute"
					referenceRunStep(t, p.ctx, db, rec, s)
					s.errorStage = "prepare"
					referenceRunStep(t, p.ctx, db, rec, referencePrepared(s))
				}
				s := referenceImplicitError("SELECT "+fn+"($1)", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad")
				s.selectRows = true
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, db, rec, referencePrepared(s))
				matched := referenceSelect("SELECT "+fn+"(VARIADIC NULL)", []string{fn}, []string{"BOOL"}, [][]any{{nil}})
				referenceRunStep(t, p.ctx, db, rec, matched)
				referenceRunStep(t, p.ctx, db, rec, referencePrepared(matched))
				args := strings.TrimSuffix(strings.Repeat("1,", 101), ",")
				limit := referenceImplicitError("SELECT "+fn+"("+args+")", referenceDiagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"})
				limit.selectRows = true
				limit.errorStage = "execute"
				referenceRunStep(t, p.ctx, db, rec, limit)
			}
		})
	}
}

func TestReferenceVisibilityInquiryDiagnosticOrdering(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []string{"Oracle", "Native"} {
		t.Run(backend, func(t *testing.T) {
			db, rec := p.postgres, p.postgresRecord
			if backend == "Native" {
				db, rec = p.memory, p.memoryRecord
			}
			referenceRunStep(t, p.ctx, db, rec, referenceExec("CREATE TABLE empty_ids(id OID)", 0))
			for _, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
				t.Run(fn, func(t *testing.T) {
					for _, tc := range []struct {
						name string
						args string
						want referenceDiagnostic
					}{
						{"missing_child", "missing_column", referenceDiagnostic{Code: "42703", Message: `column "missing_column" does not exist`}},
						{"argument_limit", strings.TrimSuffix(strings.Repeat("1,", 101), ","), referenceDiagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}},
						{"over_limit_missing_child", strings.Repeat("1,", 100) + "missing_column", referenceDiagnostic{Code: "42703", Message: `column "missing_column" does not exist`}},
					} {
						t.Run(tc.name, func(t *testing.T) {
							q := "SELECT missing_schema." + fn + "(" + tc.args + ") FROM empty_ids"
							if tc.want.Code == "42703" {
								tc.want.Position = strconv.Itoa(strings.Index(q, "missing_column") + 1)
							}
							s := referenceImplicitError(q, tc.want)
							s.selectRows = true
							s.errorStage = "execute"
							referenceRunStep(t, p.ctx, db, rec, s)
							s.errorStage = "prepare"
							referenceRunStep(t, p.ctx, db, rec, referencePrepared(s))
						})
					}
				})
			}
		})
	}
}
