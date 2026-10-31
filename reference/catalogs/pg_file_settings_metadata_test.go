package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGFileSettingsMetadata(t *testing.T) {
	pgFileSettingsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgFileSettingsInitialACL(t, p, b, b.DB, true)
		pgFileSettingsRoutineMetadata(t, p, b, b.DB)
		owner := "postgres"
		if b.PG {
			owner = "stuntgres"
		}
		steps := []fixture.Step{
			fixture.Select("SELECT attnum::int4,attname::text,atttypid::bigint,atttypmod::int4,attndims::int4,attcollation::bigint,attnotnull,attisdropped FROM pg_attribute WHERE attrelid='pg_catalog.pg_file_settings'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "atttypmod", "attndims", "attcollation", "attnotnull", "attisdropped"}, []string{"INT4", "TEXT", "INT8", "INT4", "INT4", "INT8", "BOOL", "BOOL"}, [][]any{{int64(1), "sourcefile", int64(25), int64(-1), int64(0), int64(100), false, false}, {int64(2), "sourceline", int64(23), int64(-1), int64(0), int64(0), false, false}, {int64(3), "seqno", int64(23), int64(-1), int64(0), int64(0), false, false}, {int64(4), "name", int64(25), int64(-1), int64(0), int64(100), false, false}, {int64(5), "setting", int64(25), int64(-1), int64(0), int64(100), false, false}, {int64(6), "applied", int64(16), int64(-1), int64(0), int64(0), false, false}, {int64(7), "error", int64(25), int64(-1), int64(0), int64(100), false, false}}),
			fixture.Select("SELECT c.relkind::text,c.relnamespace::bigint,c.relnatts::int4,c.relhasindex,c.relowner::regrole::text,t.typname::text,t.typtype::text,t.typrelid=c.oid AS owns_relation,a.typelem=t.oid AS owns_element,t.typnamespace=c.relnamespace AS row_namespace,a.typnamespace=c.relnamespace AS array_namespace FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_file_settings'::regclass", []string{"relkind", "relnamespace", "relnatts", "relhasindex", "relowner", "typname", "typtype", "owns_relation", "owns_element", "row_namespace", "array_namespace"}, []string{"TEXT", "INT8", "INT4", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(11), int64(7), false, owner, "pg_file_settings", "c", true, true, true, true}}),
			fixture.Select("SELECT schemaname::text,viewname::text FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_file_settings'", []string{"schemaname", "viewname"}, []string{"TEXT", "TEXT"}, [][]any{{"pg_catalog", "pg_file_settings"}}),
			fixture.Select("SELECT a.typname::text FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_file_settings'::regclass", []string{"typname"}, []string{"TEXT"}, [][]any{{"_pg_file_settings"}}),
			fixture.Select("SELECT pg_get_viewdef('pg_catalog.pg_file_settings'::regclass,false) AS nonpretty,pg_get_viewdef('pg_catalog.pg_file_settings'::regclass,true) AS pretty,definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_file_settings'", []string{"nonpretty", "pretty", "definition"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{pgFileSettingsViewDefinition, pgFileSettingsViewDefinition, pgFileSettingsViewDefinition}}),
		}
		for _, s := range steps {
			for _, prepared := range []bool{false, true} {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
	})
}
