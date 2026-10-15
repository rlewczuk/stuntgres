package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

var pgTableBuiltinNames = []string{"pg_attrdef", "pg_attribute", "pg_cast", "pg_class", "pg_constraint", "pg_database", "pg_db_role_setting", "pg_depend", "pg_description", "pg_index", "pg_language", "pg_rewrite", "pg_sequence", "pg_shdescription"}
var pgViewBuiltinNames = []string{"pg_group", "pg_indexes", "pg_prepared_xacts", "pg_roles", "pg_tables", "pg_views"}

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
			for _, name := range []string{"pg_group", "pg_indexes", "pg_prepared_xacts", "pg_roles", "pg_tables", "pg_views"} {
				views = append(views, []any{"pg_catalog", name, pgTableViewsOwner(b), pgTableViewsBuiltinDefinitions[name]})
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, pgTableViewSelect(b, "SELECT * FROM pg_catalog.pg_views WHERE schemaname='pg_catalog' AND "+pgTableViewsNames("viewname", pgViewBuiltinNames)+" ORDER BY viewname", pgViewColumns, pgViewTypes, views))
		})
	}
}

// https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/catalog/system_views.sql
// CREATE VIEW sections; pinned18.0 reconstruction observed in part tests-2.
// These describe PostgreSQL, not native-executable definitions.
var pgTableViewsBuiltinDefinitions = map[string]string{
	"pg_group": pgGroupViewDefinition,
	"pg_roles": pgRolesViewDefinition,
	"pg_indexes": ` SELECT n.nspname AS schemaname,
    c.relname AS tablename,
    i.relname AS indexname,
    t.spcname AS tablespace,
    pg_get_indexdef(i.oid) AS indexdef
   FROM ((((pg_index x
     JOIN pg_class c ON ((c.oid = x.indrelid)))
     JOIN pg_class i ON ((i.oid = x.indexrelid)))
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
     LEFT JOIN pg_tablespace t ON ((t.oid = i.reltablespace)))
  WHERE ((c.relkind = ANY (ARRAY['r'::"char", 'm'::"char", 'p'::"char"])) AND (i.relkind = ANY (ARRAY['i'::"char", 'I'::"char"])));`,
	"pg_prepared_xacts": ` SELECT p.transaction,
    p.gid,
    p.prepared,
    u.rolname AS owner,
    d.datname AS database
   FROM ((pg_prepared_xact() p(transaction, gid, prepared, ownerid, dbid)
     LEFT JOIN pg_authid u ON ((p.ownerid = u.oid)))
     LEFT JOIN pg_database d ON ((p.dbid = d.oid)));`,
	"pg_tables": ` SELECT n.nspname AS schemaname,
    c.relname AS tablename,
    pg_get_userbyid(c.relowner) AS tableowner,
    t.spcname AS tablespace,
    c.relhasindex AS hasindexes,
    c.relhasrules AS hasrules,
    c.relhastriggers AS hastriggers,
    c.relrowsecurity AS rowsecurity
   FROM ((pg_class c
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
     LEFT JOIN pg_tablespace t ON ((t.oid = c.reltablespace)))
  WHERE (c.relkind = ANY (ARRAY['r'::"char", 'p'::"char"]));`,
	"pg_views": ` SELECT n.nspname AS schemaname,
    c.relname AS viewname,
    pg_get_userbyid(c.relowner) AS viewowner,
    pg_get_viewdef(c.oid) AS definition
   FROM (pg_class c
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
  WHERE (c.relkind = 'v'::"char");`,
}
