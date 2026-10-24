package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// This is the independently selected consumer surface, not a production registry
// enumeration. PostgreSQL has other views outside this represented surface.
var ivBuiltinNames = []struct{ schema, name string }{
	{"pg_catalog", "pg_views"}, {"pg_catalog", "pg_prepared_xacts"}, {"pg_catalog", "pg_indexes"},
	{"pg_catalog", "pg_rules"},
	{"pg_catalog", "pg_tables"}, {"pg_catalog", "pg_timezone_abbrevs"}, {"pg_catalog", "pg_timezone_names"},
	{"pg_catalog", "pg_matviews"}, {"pg_catalog", "pg_roles"}, {"pg_catalog", "pg_group"},
	{"pg_catalog", "pg_stat_all_tables"}, {"pg_catalog", "pg_stat_sys_tables"}, {"pg_catalog", "pg_stat_user_tables"},
	{"pg_catalog", "pg_stat_progress_vacuum"}, {"pg_catalog", "pg_user"}, {"pg_catalog", "pg_sequences"},
	{"pg_catalog", "pg_locks"}, {"pg_catalog", "pg_cursors"}, {"pg_catalog", "pg_available_extensions"},
	{"pg_catalog", "pg_available_extension_versions"}, {"pg_catalog", "pg_user_mappings"}, {"pg_catalog", "pg_settings"},
	{"information_schema", "table_privileges"}, {"information_schema", "data_type_privileges"},
	{"information_schema", "user_mappings"}, {"information_schema", "key_column_usage"},
	{"information_schema", "routine_sequence_usage"}, {"information_schema", "character_sets"},
	{"information_schema", "constraint_column_usage"}, {"information_schema", "enabled_roles"},
	{"information_schema", "view_table_usage"}, {"information_schema", "column_column_usage"},
	{"information_schema", "domain_constraints"}, {"information_schema", "sequences"},
	{"information_schema", "role_column_grants"}, {"information_schema", "columns"},
	{"information_schema", "role_table_grants"},
	{"information_schema", "domains"}, {"information_schema", "views"},
	{"information_schema", "tables"},
}

func TestReferenceInfoViewsBuiltins(t *testing.T) {
	for _, mode := range []struct {
		name        string
		path        string
		definitions map[string]string
		quote       bool
	}{
		{"default", "public", ivBuiltinDefinitions, false},
		{"information_schema_path", "public,information_schema,pg_catalog", ivBuiltinPathDefinitions, false},
		{"shadow", "public,pg_catalog", ivBuiltinShadowDefinitions, false},
		{"quote_default", "public", ivBuiltinQuoteDefaultDefinitions, true},
		{"quote_information_schema_path", "public,information_schema,pg_catalog", ivBuiltinQuotePathDefinitions, true},
		{"quote_shadow", "public,pg_catalog", ivBuiltinQuoteShadowDefinitions, true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			for _, candidate := range ivBuiltinNames {
				t.Run(candidate.name, func(t *testing.T) {
					ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
						if mode.name == "shadow" || mode.name == "quote_shadow" {
							for _, q := range []string{
								"CREATE TABLE public.pg_authid(n integer)",
								"CREATE TABLE public.pg_class(n integer)",
								"CREATE TABLE public.pg_namespace(n integer)",
								"CREATE TABLE public.pg_attribute(n integer)",
								"CREATE FUNCTION public.pg_get_userbyid(oid) RETURNS name BEGIN ATOMIC SELECT 'shadow'::name; END",
							} {
								run(fixture.Exec(q, 0))
							}
						}
						run(fixture.Exec("SET search_path="+mode.path, 0))
						if mode.quote {
							run(fixture.Exec("SET quote_all_identifiers=on", 0))
						}
						update, insert := "NO", "NO"
						if candidate.name == "pg_group" {
							update, insert = "YES", "YES"
						}
						key := candidate.schema + "." + candidate.name
						definition, ok := mode.definitions[key]
						if !ok {
							t.Fatalf("missing independent %s definition for %s", mode.name, key)
						}
						s := ivStep(b.PG, ivSelect, [][]any{ivRow(database, candidate.schema, candidate.name, definition, "NONE", update, insert)}, candidate.schema, candidate.name)
						run(s)
						run(fixture.Prepared(s))
						ivHeld(t, p, b, c, ivSelect)(s)
						summarySQL := "SELECT definition FROM pg_catalog.pg_views WHERE schemaname=$1 AND viewname=$2"
						summary := fixture.Select(summarySQL, []string{"definition"}, []string{"TEXT"}, [][]any{{definition}}, candidate.schema, candidate.name)
						run(summary)
						run(fixture.Prepared(summary))
						ivHeld(t, p, b, c, summarySQL)(summary)
					})
				})
			}
		})
	}
	t.Run("held_display_settings", func(t *testing.T) {
		ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("SET search_path=public", 0))
			held := ivHeld(t, p, b, c, ivSelect)
			summarySQL := "SELECT definition FROM pg_catalog.pg_views WHERE schemaname=$1 AND viewname=$2"
			heldSummary := ivHeld(t, p, b, c, summarySQL)
			for _, mode := range []struct {
				path, quote string
				definitions map[string]string
			}{
				{"public", "off", ivBuiltinDefinitions},
				{"public", "on", ivBuiltinQuoteDefaultDefinitions},
				{"public,information_schema,pg_catalog", "on", ivBuiltinQuotePathDefinitions},
				{"public,information_schema,pg_catalog", "off", ivBuiltinPathDefinitions},
			} {
				run(fixture.Exec("SET search_path="+mode.path, 0))
				run(fixture.Exec("SET quote_all_identifiers="+mode.quote, 0))
				for _, candidate := range []struct{ schema, name string }{{"pg_catalog", "pg_group"}, {"pg_catalog", "pg_rules"}, {"information_schema", "enabled_roles"}, {"information_schema", "role_table_grants"}, {"information_schema", "columns"}, {"information_schema", "domains"}, {"information_schema", "tables"}} {
					update, insert := "NO", "NO"
					if candidate.name == "pg_group" {
						update, insert = "YES", "YES"
					}
					definition := mode.definitions[candidate.schema+"."+candidate.name]
					held(ivStep(b.PG, ivSelect, [][]any{ivRow(database, candidate.schema, candidate.name, definition, "NONE", update, insert)}, candidate.schema, candidate.name))
					heldSummary(fixture.Select(summarySQL, []string{"definition"}, []string{"TEXT"}, [][]any{{definition}}, candidate.schema, candidate.name))
				}
			}
		})
	})
	t.Run("nonowner_disclosure", func(t *testing.T) {
		ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE ROLE iv_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION iv_reader", 0))
			held := ivHeld(t, p, b, c, ivSelect)
			for _, candidate := range ivBuiltinNames {
				update, insert := "NO", "NO"
				if candidate.name == "pg_group" {
					update, insert = "YES", "YES"
				}
				s := ivStep(b.PG, ivSelect, [][]any{ivRow(database, candidate.schema, candidate.name, nil, "NONE", update, insert)}, candidate.schema, candidate.name)
				run(s)
				run(fixture.Prepared(s))
				held(s)
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		})
	})
	t.Run("column_subsets", func(t *testing.T) {
		ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ string) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, tc := range []struct{ name, query, update, insert string }{
				{"iv_group_direct", "SELECT groname FROM pg_catalog.pg_group", "YES", "YES"},
				{"iv_group_computed", "SELECT grolist FROM pg_catalog.pg_group", "NO", "NO"},
				{"iv_group_constant", "SELECT 1 AS n FROM pg_catalog.pg_group", "NO", "NO"},
				{"iv_settings_direct", "SELECT setting FROM pg_catalog.pg_settings", "NO", "NO"},
				{"iv_settings_constant", "SELECT 1 AS n FROM pg_catalog.pg_settings", "NO", "NO"},
				{"iv_enabled_direct", "SELECT role_name FROM information_schema.enabled_roles", "NO", "NO"},
				{"iv_catalog_table", "SELECT relname FROM pg_catalog.pg_class", "YES", "YES"},
			} {
				run(fixture.Exec("CREATE VIEW "+tc.name+" AS "+tc.query, 0))
				s := ivFlags(ivFlagSelect, ivFlagRow(tc.name, tc.update, tc.insert), tc.name)
				run(s)
				run(fixture.Prepared(s))
				ivHeld(t, p, b, c, ivFlagSelect)(s)
			}
		})
	})
}
