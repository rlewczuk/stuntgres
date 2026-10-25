package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

var pgTableBuiltinNames = []string{"pg_aggregate", "pg_attrdef", "pg_attribute", "pg_cast", "pg_class", "pg_constraint", "pg_database", "pg_db_role_setting", "pg_depend", "pg_description", "pg_index", "pg_language", "pg_range", "pg_rewrite", "pg_sequence", "pg_shdescription", "pg_transform", "pg_ts_config"}

func TestReferencePGTableViewsBuiltins(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var expected [][]any
			for _, name := range pgTableBuiltinNames {
				row := fixture.PGTableRow("pg_catalog", name, fixture.PGTableViewsOwner(b), true)
				if name == "pg_database" || name == "pg_db_role_setting" || name == "pg_shdescription" {
					row[3] = "pg_global"
				}
				expected = append(expected, row)
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.PGTableViewSelect(b, "SELECT * FROM pg_catalog.pg_tables WHERE schemaname='pg_catalog' AND "+fixture.PGTableViewsNames("tablename", pgTableBuiltinNames)+" ORDER BY tablename", fixture.PGTableColumns, fixture.PGTableTypes, expected))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.PGTableViewSelect(b, "SELECT * FROM pg_catalog.pg_tables WHERE schemaname='pg_catalog' AND "+fixture.PGTableViewsNames("tablename", fixture.PGViewBuiltinNames), fixture.PGTableColumns, fixture.PGTableTypes, nil))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.PGTableViewSelect(b, "SELECT * FROM pg_catalog.pg_views WHERE schemaname='pg_catalog' AND "+fixture.PGTableViewsNames("viewname", pgTableBuiltinNames), fixture.PGViewColumns, fixture.PGViewTypes, nil))
			var views [][]any
			for _, name := range fixture.PGViewBuiltinNames {
				views = append(views, []any{"pg_catalog", name, fixture.PGTableViewsOwner(b), ivBuiltinDefinitions["pg_catalog."+name]})
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.PGTableViewSelect(b, "SELECT * FROM pg_catalog.pg_views WHERE schemaname='pg_catalog' AND "+fixture.PGTableViewsNames("viewname", fixture.PGViewBuiltinNames)+" ORDER BY viewname", fixture.PGViewColumns, fixture.PGViewTypes, views))
			var infoNames []string
			var infoViews [][]any
			for _, name := range ivBuiltinNames {
				if name.schema != "information_schema" {
					continue
				}
				infoNames = append(infoNames, name.name)
				infoViews = append(infoViews, []any{name.schema, name.name, fixture.PGTableViewsOwner(b), ivBuiltinDefinitions[name.schema+"."+name.name]})
			}
			info := fixture.PGTableViewSelect(b, "SELECT * FROM pg_catalog.pg_views WHERE schemaname='information_schema' AND "+fixture.PGTableViewsNames("viewname", infoNames), fixture.PGViewColumns, fixture.PGViewTypes, infoViews)
			info.Bag = true
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, info)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(info))
		})
	}
}
