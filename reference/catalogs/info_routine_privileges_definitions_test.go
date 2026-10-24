package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutinePrivilegesDefinitions(t *testing.T) {
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
			rpScenario(t, func(c *rrgCase) {
				if mode.shadow {
					c.exec("CREATE TABLE public.pg_proc(n integer)", "CREATE TABLE public.pg_namespace(n integer)", "CREATE TABLE public.pg_authid(n integer)")
				}
				c.exec("SET search_path=" + mode.path)
				if mode.quote {
					c.exec("SET quote_all_identifiers=on")
				}
				definition := mode.definitions["information_schema.routine_privileges"]
				if mode.shadow {
					// The shared shadow inventory hides namespace/authid. This witness
					// additionally hides pg_proc; only its FROM binding is qualified,
					// never the pg_proc alias in the independent released expression.
					from, to := "FROM pg_proc)", "FROM pg_catalog.pg_proc)"
					if mode.quote {
						from, to = `FROM "pg_proc")`, `FROM "pg_catalog"."pg_proc")`
					}
					if strings.Count(definition, from) != 1 {
						c.t.Fatal("missing independent pg_proc binding")
					}
					definition = strings.Replace(definition, from, to, 1)
				}
				s := ivStep(c.b.PG, ivSelect, [][]any{ivRow(c.database, "information_schema", "routine_privileges", definition, "NONE", "NO", "NO")}, "information_schema", "routine_privileges")
				c.run(c.conn, s)
				c.run(c.conn, fixture.Prepared(s))
				c.run(c.hold(c.conn, ivSelect), s)
				for _, q := range []string{
					"SELECT definition FROM pg_catalog.pg_views WHERE schemaname='information_schema' AND viewname='routine_privileges'",
					"SELECT pg_get_viewdef('information_schema.routine_privileges'::regclass) AS definition",
					"SELECT pg_get_viewdef('information_schema.routine_privileges'::text) AS definition",
				} {
					s := fixture.Select(q, []string{"definition"}, []string{"TEXT"}, [][]any{{definition}})
					c.run(c.conn, s)
					c.run(c.conn, fixture.Prepared(s))
					c.run(c.hold(c.conn, q), s)
				}
			})
		})
	}
}
