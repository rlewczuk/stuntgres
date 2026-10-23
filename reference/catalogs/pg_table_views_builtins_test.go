package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

var pgTableBuiltinNames = []string{"pg_aggregate", "pg_attrdef", "pg_attribute", "pg_cast", "pg_class", "pg_constraint", "pg_database", "pg_db_role_setting", "pg_depend", "pg_description", "pg_index", "pg_language", "pg_range", "pg_rewrite", "pg_sequence", "pg_shdescription", "pg_transform", "pg_ts_config"}
var pgViewBuiltinNames = []string{"pg_available_extension_versions", "pg_available_extensions", "pg_cursors", "pg_group", "pg_indexes", "pg_locks", "pg_matviews", "pg_prepared_xacts", "pg_roles", "pg_rules", "pg_sequences", "pg_settings", "pg_stat_all_tables", "pg_stat_progress_vacuum", "pg_stat_sys_tables", "pg_stat_user_tables", "pg_tables", "pg_timezone_abbrevs", "pg_timezone_names", "pg_user", "pg_user_mappings", "pg_views"}

func pgTableViewsNames(column string, names []string) string {
	terms := make([]string, len(names))
	for i, name := range names {
		terms[i] = column + "='" + name + "'"
	}
	return "(" + strings.Join(terms, " OR ") + ")"
}

func TestReferencePGTableViewsBuiltins(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var expected [][]any
			for _, name := range pgTableBuiltinNames {
				row := pgTableRow("pg_catalog", name, pgTableViewsOwner(b), true)
				if name == "pg_database" || name == "pg_db_role_setting" || name == "pg_shdescription" {
					row[3] = "pg_global"
				}
				expected = append(expected, row)
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, pgTableViewSelect(b, "SELECT * FROM pg_catalog.pg_tables WHERE schemaname='pg_catalog' AND "+pgTableViewsNames("tablename", pgTableBuiltinNames)+" ORDER BY tablename", pgTableColumns, pgTableTypes, expected))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, pgTableViewSelect(b, "SELECT * FROM pg_catalog.pg_tables WHERE schemaname='pg_catalog' AND "+pgTableViewsNames("tablename", pgViewBuiltinNames), pgTableColumns, pgTableTypes, nil))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, pgTableViewSelect(b, "SELECT * FROM pg_catalog.pg_views WHERE schemaname='pg_catalog' AND "+pgTableViewsNames("viewname", pgTableBuiltinNames), pgViewColumns, pgViewTypes, nil))
			var views [][]any
			for _, name := range pgViewBuiltinNames {
				views = append(views, []any{"pg_catalog", name, pgTableViewsOwner(b), ivBuiltinDefinitions["pg_catalog."+name]})
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, pgTableViewSelect(b, "SELECT * FROM pg_catalog.pg_views WHERE schemaname='pg_catalog' AND "+pgTableViewsNames("viewname", pgViewBuiltinNames)+" ORDER BY viewname", pgViewColumns, pgViewTypes, views))
			var infoNames []string
			var infoViews [][]any
			for _, name := range ivBuiltinNames {
				if name.schema != "information_schema" {
					continue
				}
				infoNames = append(infoNames, name.name)
				infoViews = append(infoViews, []any{name.schema, name.name, pgTableViewsOwner(b), ivBuiltinDefinitions[name.schema+"."+name.name]})
			}
			info := pgTableViewSelect(b, "SELECT * FROM pg_catalog.pg_views WHERE schemaname='information_schema' AND "+pgTableViewsNames("viewname", infoNames), pgViewColumns, pgViewTypes, infoViews)
			info.Bag = true
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, info)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(info))
		})
	}
}
