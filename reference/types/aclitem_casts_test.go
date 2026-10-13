package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceACLItemCasts(t *testing.T) {
	aclitemScenario(t, []string{"acl_cast_owner"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE DOMAIN public.acl_cast_source AS aclitem", 0))
		run(fixture.Exec("CREATE DOMAIN public.acl_cast_v3 AS varchar(3)", 0))
		run(fixture.Exec("CREATE DOMAIN public.acl_cast_c3 AS char(3)", 0))
		const input = "=r/acl_cast_owner"
		for _, tc := range []struct{ source, target, typ string }{
			{"aclitem", "varchar(3)", "VARCHAR"},
			{"aclitem", "char(3)", "BPCHAR"},
			{"public.acl_cast_source", "varchar(3)", "VARCHAR"},
			{"public.acl_cast_source", "char(3)", "BPCHAR"},
			// Domain results are exposed through TEXT so these witnesses isolate
			// destination sizing, independently of domain metadata transport.
			{"aclitem", "public.acl_cast_v3", "TEXT"},
			{"aclitem", "public.acl_cast_c3", "TEXT"},
		} {
			for _, prepared := range []bool{false, true} {
				for _, bound := range []bool{false, true} {
					for _, null := range []bool{false, true} {
						expression := fixture.ArrayReferenceQuote(input)
						var args []any
						var want any = "=r/"
						if null {
							expression, want = "NULL", nil
						}
						if bound {
							expression = "$1"
							if null {
								args = []any{nil}
							} else {
								args = []any{input}
							}
						}
						expression = "CAST(CAST(" + expression + " AS " + tc.source + ") AS " + tc.target + ")"
						if tc.typ == "TEXT" {
							expression = "CAST(" + expression + " AS text)"
						}
						s := fixture.Select("SELECT "+expression+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{want}}, args...)
						s.Prepared = prepared
						run(s)
					}
				}
			}
		}
		for _, tc := range []struct{ table, target, message string }{
			{"acl_cast_assign_v", "varchar(3)", "value too long for type character varying(3)"},
			{"acl_cast_assign_c", "char(3)", "value too long for type character(3)"},
			{"acl_cast_assign_dv", "public.acl_cast_v3", "value too long for type character varying(3)"},
			{"acl_cast_assign_dc", "public.acl_cast_c3", "value too long for type character(3)"},
		} {
			run(fixture.Exec("CREATE TABLE public."+tc.table+" (id INT,v "+tc.target+")", 0))
			run(fixture.Exec("INSERT INTO public."+tc.table+" VALUES (1,NULL)", 1))
			for _, prepared := range []bool{false, true} {
				for _, bound := range []bool{false, true} {
					expression := fixture.ArrayReferenceQuote(input)
					var args []any
					if bound {
						expression, args = "$1", []any{input}
					}
					s := fixture.ErrorDiagnostic("INSERT INTO public."+tc.table+" VALUES (2,CAST("+expression+" AS aclitem))", aclitemDiagnostic("22001", tc.message), args...)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					run(fixture.Select("SELECT id,v::text AS v FROM public."+tc.table+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}}))
				}
			}
		}
	})
}
