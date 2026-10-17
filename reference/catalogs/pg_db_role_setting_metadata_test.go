package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func pgDBRoleSettingFacts(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	steps := []fixture.Step{
		fixture.Select("SELECT oid,relnamespace,relkind,relnatts,relhasindex,relisshared,reltablespace FROM pg_class WHERE oid=2964::oid", []string{"oid", "relnamespace", "relkind", "relnatts", "relhasindex", "relisshared", "reltablespace"}, []string{"OID", "OID", "CHAR", "INT2", "BOOL", "BOOL", "OID"}, [][]any{{int64(2964), int64(11), "r", int64(3), true, true, int64(1664)}}),
		fixture.Select("SELECT reltype='pg_catalog.pg_db_role_setting'::regtype::oid AS same_type FROM pg_class WHERE oid=2964::oid", []string{"same_type"}, []string{"BOOL"}, [][]any{{true}}),
		fixture.Select("SELECT 'pg_catalog.pg_db_role_setting[]'::regtype::oid='pg_catalog._pg_db_role_setting'::regtype::oid AS same_array,'pg_catalog.pg_db_role_setting'::regtype::oid>0::oid AS positive_row,'pg_catalog.pg_db_role_setting[]'::regtype::oid>0::oid AS positive_array,'pg_catalog.pg_db_role_setting[]'::regtype::oid<>'pg_catalog.pg_db_role_setting'::regtype::oid AS distinct_types", []string{"same_array", "positive_row", "positive_array", "distinct_types"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}),
		fixture.Select("SELECT schemaname,tablename,tableowner,tablespace,hasindexes,hasrules,hastriggers,rowsecurity FROM pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_db_role_setting'", pgTableColumns, pgTableTypes, [][]any{{"pg_catalog", "pg_db_role_setting", pgTableViewsOwner(b), "pg_global", true, false, false, false}}),
		fixture.Select("SELECT * FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_db_role_setting'", pgViewColumns, pgViewTypes, nil),
		fixture.Select("SELECT attrelid,attnum,attname,atttypid,attnotnull,atttypmod,attcollation,attisdropped FROM pg_attribute WHERE attrelid=2964::oid AND attnum>0 ORDER BY attnum", []string{"attrelid", "attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attcollation", "attisdropped"}, []string{"OID", "INT2", "NAME", "OID", "BOOL", "INT4", "OID", "BOOL"}, [][]any{
			{int64(2964), int64(1), "setdatabase", int64(26), true, int64(-1), int64(0), false},
			{int64(2964), int64(2), "setrole", int64(26), true, int64(-1), int64(0), false},
			{int64(2964), int64(3), "setconfig", int64(1009), false, int64(-1), int64(950), false},
		}),
	}
	steps = append(steps,
		fixture.Select("SELECT t.typrelid=2964::oid AS same_relation,a.typelem=t.oid AS same_element,a.oid='pg_catalog.pg_db_role_setting[]'::regtype::oid AS same_array FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid='pg_catalog.pg_db_role_setting'::regtype::oid", []string{"same_relation", "same_element", "same_array"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}),
	)
	for _, s := range steps {
		// Reuse the catalog codec assertions, including pq's raw OID/NAME carriers.
		pgDatabaseCheck(t, p, b, b.DB, s)
		pgDatabaseCheck(t, p, b, b.DB, fixture.Prepared(s))
		held := fixture.ViewHold(t, p, b, s.Query)
		pgDatabaseCheck(t, p, b, held, s)
		pgDatabaseCheck(t, p, b, held, s)
	}
}

func TestReferencePGDBRoleSettingMetadata(t *testing.T) {
	pgDBRoleSettingScenario(t, pgDBRoleSettingFacts)
}

func TestReferencePGDBRoleSettingSmoke(t *testing.T) {
	t.Run("Shape", TestReferencePGDBRoleSettingShape)
	t.Run("Metadata", TestReferencePGDBRoleSettingMetadata)
	t.Run("SessionExclusion", TestReferencePGDBRoleSettingSessionVisibility)
	t.Run("NativeReplacement", TestReferencePGDBRoleSettingControls)
}
