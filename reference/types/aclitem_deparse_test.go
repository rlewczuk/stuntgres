package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceACLItemDeparse(t *testing.T) {
	aclitemScenario(t, []string{"acl_deparse_owner"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, tc := range []struct {
			name, expression, definition string
			value                        any
		}{
			{"acl_deparse_scalar", "'=r/acl_deparse_owner'::aclitem", " SELECT '=r/acl_deparse_owner'::aclitem AS a;", "=r/acl_deparse_owner"},
			{"acl_deparse_null", "NULL::aclitem", " SELECT NULL::aclitem AS a;", nil},
		} {
			run(fixture.Exec("CREATE VIEW public."+tc.name+" AS SELECT "+tc.expression+" AS a", 0))
			for _, prepared := range []bool{false, true} {
				for _, typ := range []string{"text", "regclass"} {
					for _, pretty := range []string{"", ",false", ",true"} {
						for _, bound := range []bool{false, true} {
							argument := fixture.ArrayReferenceQuote("public." + tc.name)
							var args []any
							if bound {
								argument, args = "$1", []any{"public." + tc.name}
							}
							s := fixture.Select("SELECT pg_get_viewdef("+argument+"::"+typ+pretty+") AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{tc.definition}}, args...)
							s.Prepared = prepared
							run(s)
						}
					}
				}
			}
			// Replay the independently specified definition, not text learned from
			// either backend, and require the same public annotation and datum.
			replay := tc.name + "_copy"
			run(fixture.Exec("CREATE VIEW public."+replay+" AS"+tc.definition, 0))
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT pg_get_viewdef('public."+replay+"'::regclass,true) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{tc.definition}})
				s.Prepared = prepared
				run(s)
				s = aclitemSelect("SELECT a FROM public."+replay, [][]any{{tc.value}})
				s.Prepared = prepared
				run(s)
			}
		}
	})
}
