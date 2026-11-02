package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnPrivilegesDefinitions(t *testing.T) {
	for _, shadow := range []bool{false, true} {
		name := "settings"
		if shadow {
			name = "shadow_settings"
		}
		t.Run(name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := conn.Close(); err != nil {
							t.Error(err)
						}
					})
					_, database := fixture.RoleGrantEnvironment(t, p, conn)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
					if shadow {
						for _, q := range []string{"CREATE TABLE public.pg_authid(n integer)", "CREATE TABLE public.pg_class(n integer)", "CREATE TABLE public.pg_namespace(n integer)", "CREATE TABLE public.pg_attribute(n integer)"} {
							run(fixture.Exec(q, 0))
						}
					}
					queries := []string{
						"SELECT definition FROM pg_catalog.pg_views WHERE schemaname='information_schema' AND viewname='column_privileges'",
						"SELECT pg_get_viewdef('information_schema.column_privileges'::regclass) AS definition",
						"SELECT pg_get_viewdef('information_schema.column_privileges'::text) AS definition",
					}
					heldViews := ivHeld(t, p, b, conn, ivSelect)
					heldDefinitions := make([]func(fixture.Step), len(queries))
					for i, q := range queries {
						heldDefinitions[i] = ivHeld(t, p, b, conn, q)
					}
					for _, mode := range []struct {
						path, quote string
						definitions map[string]string
					}{
						{"public", "off", ivBuiltinDefinitions},
						{"public,information_schema,pg_catalog", "off", ivBuiltinPathDefinitions},
						{"public", "on", ivBuiltinQuoteDefaultDefinitions},
						{"public,information_schema,pg_catalog", "on", ivBuiltinQuotePathDefinitions},
						{"public", "off", ivBuiltinDefinitions},
					} {
						definitions := mode.definitions
						path := mode.path
						if shadow {
							path = "public,pg_catalog"
							definitions = ivBuiltinShadowDefinitions
							if mode.quote == "on" {
								definitions = ivBuiltinQuoteShadowDefinitions
							}
						}
						run(fixture.Exec("SET search_path="+path, 0))
						run(fixture.Exec("SET quote_all_identifiers="+mode.quote, 0))
						definition := definitions["information_schema.column_privileges"]
						if definition == "" {
							t.Fatal("missing independent column_privileges definition")
						}
						s := ivStep(b.PG, ivSelect, [][]any{ivRow(database, "information_schema", "column_privileges", definition, "NONE", "NO", "NO")}, "information_schema", "column_privileges")
						run(s)
						run(fixture.Prepared(s))
						heldViews(s)
						for i, q := range queries {
							s := fixture.Select(q, []string{"definition"}, []string{"TEXT"}, [][]any{{definition}})
							run(s)
							run(fixture.Prepared(s))
							heldDefinitions[i](s)
						}
					}
				})
			}
		})
	}
}
