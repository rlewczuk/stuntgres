package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoUsagePrivilegesDefinitions(t *testing.T) {
	upScenario(t, func(c *upCase) {
		const viewsQuery = "SELECT * FROM information_schema.views WHERE table_schema=$1 AND table_name=$2"
		const summaryQuery = "SELECT definition FROM pg_catalog.pg_views WHERE schemaname=$1 AND viewname=$2"
		const oidQuery = "SELECT pg_get_viewdef($1::oid) AS definition"
		const textQuery = "SELECT pg_get_viewdef($1::text) AS definition"
		var oid int64
		if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT c.oid::bigint FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='information_schema' AND c.relname='usage_privileges'").Scan(&oid); err != nil {
			c.t.Fatal(err)
		}
		views, summary := c.hold(c.conn, viewsQuery), c.hold(c.conn, summaryQuery)
		byOID, byText := c.hold(c.conn, oidQuery), c.hold(c.conn, textQuery)
		shadowed := false
		for _, mode := range []struct {
			path, quote, definition string
			shadow                  bool
		}{
			{"public", "off", upDefinition, false},
			{"public,information_schema,pg_catalog", "off", upPathDefinition, false},
			{"public", "on", upQuotedDefinition, false},
			{"public,information_schema,pg_catalog", "on", upQuotedPathDefinition, false},
			{"public,pg_catalog", "off", upShadowDefinition, true},
			{"public,pg_catalog", "on", upQuotedShadowDefinition, true},
		} {
			if mode.shadow && !shadowed {
				c.exec("CREATE TABLE public.pg_authid(n integer)", "CREATE TABLE public.pg_class(n integer)", "CREATE TABLE public.pg_namespace(n integer)")
				shadowed = true
			}
			c.exec("SET search_path="+mode.path, "SET quote_all_identifiers="+mode.quote)
			view := fixture.Select(viewsQuery,
				[]string{"table_catalog", "table_schema", "table_name", "view_definition", "check_option", "is_updatable", "is_insertable_into", "is_trigger_updatable", "is_trigger_deletable", "is_trigger_insertable_into"},
				[]string{"NAME", "NAME", "NAME", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR"},
				[][]any{{c.database, "information_schema", "usage_privileges", mode.definition, "NONE", "NO", "NO", "NO", "NO", "NO"}}, "information_schema", "usage_privileges")
			for _, route := range []struct {
				step fixture.Step
				held fixture.HeldStatement
			}{
				{view, views},
				{fixture.Select(summaryQuery, []string{"definition"}, []string{"TEXT"}, [][]any{{mode.definition}}, "information_schema", "usage_privileges"), summary},
				{fixture.Select(oidQuery, []string{"definition"}, []string{"TEXT"}, [][]any{{mode.definition}}, oid), byOID},
				{fixture.Select(textQuery, []string{"definition"}, []string{"TEXT"}, [][]any{{mode.definition}}, "information_schema.usage_privileges"), byText},
			} {
				c.run(c.conn, route.step)
				c.run(c.conn, fixture.Prepared(route.step))
				c.run(route.held, route.step)
			}
		}
	})
}
