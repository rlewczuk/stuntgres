package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Keep the new consumer in the oracle-first family as well as the complete
// represented-view inventory. These are literal released-SQL expectations, not
// definitions read back from either backend to manufacture expected values.
func TestReferenceInfoRoleRoutineGrantsDefinitions(t *testing.T) {
	for _, mode := range []struct {
		name, path    string
		quote, shadow bool
		definitions   map[string]string
	}{
		{"default", "public", false, false, ivBuiltinDefinitions},
		{"information_schema_path", "public,information_schema,pg_catalog", false, false, ivBuiltinPathDefinitions},
		{"shadow", "public,pg_catalog", false, true, ivBuiltinShadowDefinitions},
		{"quote_default", "public", true, false, ivBuiltinQuoteDefaultDefinitions},
		{"quote_information_schema_path", "public,information_schema,pg_catalog", true, false, ivBuiltinQuotePathDefinitions},
		{"quote_shadow", "public,pg_catalog", true, true, ivBuiltinQuoteShadowDefinitions},
	} {
		t.Run(mode.name, func(t *testing.T) {
			rrgScenario(t, func(c *rrgCase) {
				if mode.shadow {
					c.exec("CREATE TABLE public.routine_privileges(n integer)", "CREATE TABLE public.enabled_roles(n integer)")
				}
				c.exec("CREATE VIEW public.rrg_deparse_view AS SELECT 7 AS result", "CREATE TABLE public.rrg_deparse_table(n integer)")
				c.exec("SET search_path=" + mode.path)
				if mode.quote {
					c.exec("SET quote_all_identifiers=on")
				}
				definition := mode.definitions["information_schema.role_routine_grants"]
				s := ivStep(c.b.PG, ivSelect, [][]any{ivRow(c.database, "information_schema", "role_routine_grants", definition, "NONE", "NO", "NO")}, "information_schema", "role_routine_grants")
				c.run(c.conn, s)
				c.run(c.conn, fixture.Prepared(s))
				c.run(c.hold(c.conn, ivSelect), s)
				for _, q := range []string{"SELECT definition FROM pg_catalog.pg_views WHERE schemaname='information_schema' AND viewname='role_routine_grants'", "SELECT pg_get_viewdef('information_schema.role_routine_grants'::regclass) AS definition", "SELECT pg_get_viewdef('information_schema.role_routine_grants'::text) AS definition"} {
					s := fixture.Select(q, []string{"definition"}, []string{"TEXT"}, [][]any{{definition}})
					c.run(c.conn, s)
					c.run(c.conn, fixture.Prepared(s))
					c.run(c.hold(c.conn, q), s)
				}
				storedDefinition := " SELECT 7 AS result;"
				if mode.quote {
					storedDefinition = ` SELECT 7 AS "result";`
				}
				for _, tc := range []struct {
					target string
					want   any
				}{
					{"public.rrg_deparse_view", storedDefinition},
					{"public.rrg_deparse_table", nil},
				} {
					for _, inputType := range []string{"regclass", "text"} {
						q := "SELECT pg_get_viewdef('" + tc.target + "'::" + inputType + ") AS definition"
						s := fixture.Select(q, []string{"definition"}, []string{"TEXT"}, [][]any{{tc.want}})
						c.run(c.conn, s)
						c.run(c.conn, fixture.Prepared(s))
						c.run(c.hold(c.conn, q), s)
					}
				}
			})
		})
	}
}
