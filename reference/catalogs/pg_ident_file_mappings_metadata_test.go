package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func identCatalogMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	owner := "postgres"
	if b.PG {
		owner = "stuntgres"
	}
	attributes := make([][]any, 7)
	for i, name := range identColumns {
		oid, collation := int64(25), int64(100)
		if i == 0 || i == 2 {
			oid, collation = 23, 0
		}
		attributes[i] = []any{int64(i + 1), name, oid, int64(-1), int64(0), collation, false, false}
	}
	steps := []fixture.Step{
		fixture.Select("SELECT attnum::int4,attname::text,atttypid::bigint,atttypmod::int4,attndims::int4,attcollation::bigint,attnotnull,attisdropped FROM pg_attribute WHERE attrelid='pg_catalog.pg_ident_file_mappings'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "atttypmod", "attndims", "attcollation", "attnotnull", "attisdropped"}, []string{"INT4", "TEXT", "INT8", "INT4", "INT4", "INT8", "BOOL", "BOOL"}, attributes),
		fixture.Select("SELECT c.relkind::text,c.relnamespace::bigint,c.relnatts::int4,c.relhasindex,c.relowner::regrole::text,t.typname::text,t.typtype::text,t.typrelid=c.oid AS owns_relation,a.typelem=t.oid AS owns_element,t.typnamespace=c.relnamespace AS row_namespace,a.typnamespace=c.relnamespace AS array_namespace FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_ident_file_mappings'::regclass", []string{"relkind", "relnamespace", "relnatts", "relhasindex", "relowner", "typname", "typtype", "owns_relation", "owns_element", "row_namespace", "array_namespace"}, []string{"TEXT", "INT8", "INT4", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(11), int64(7), false, owner, "pg_ident_file_mappings", "c", true, true, true, true}}),
		fixture.Select("SELECT schemaname::text,viewname::text FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_ident_file_mappings'", []string{"schemaname", "viewname"}, []string{"TEXT", "TEXT"}, [][]any{{"pg_catalog", "pg_ident_file_mappings"}}),
		fixture.Select("SELECT oid::bigint,proname::text,pronamespace::bigint,proowner::regrole::text,prolang::bigint,procost,prorows,provariadic::bigint,prosupport::text,prokind::text,prosecdef,proleakproof,proisstrict,proretset,provolatile::text,proparallel::text,pronargs::int4,pronargdefaults::int4,prorettype::bigint,proargtypes::text,proallargtypes::text,proargmodes::text,proargnames::text,proargdefaults::text,protrftypes::text,prosrc,probin,prosqlbody::text,proconfig::text,proacl::text FROM pg_proc WHERE oid=6250",
			[]string{"oid", "proname", "pronamespace", "proowner", "prolang", "procost", "prorows", "provariadic", "prosupport", "prokind", "prosecdef", "proleakproof", "proisstrict", "proretset", "provolatile", "proparallel", "pronargs", "pronargdefaults", "prorettype", "proargtypes", "proallargtypes", "proargmodes", "proargnames", "proargdefaults", "protrftypes", "prosrc", "probin", "prosqlbody", "proconfig", "proacl"},
			[]string{"INT8", "TEXT", "INT8", "TEXT", "INT8", "FLOAT4", "FLOAT4", "INT8", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "INT4", "INT4", "INT8", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"},
			[][]any{{int64(6250), "pg_ident_file_mappings", int64(11), owner, int64(12), float64(1), float64(1000), int64(0), "-", "f", false, false, true, true, "v", "s", int64(0), int64(0), int64(2249), "", "{23,25,23,25,25,25,25}", "{o,o,o,o,o,o,o}", "{map_number,file_name,line_number,map_name,sys_name,pg_username,error}", nil, nil, "pg_ident_file_mappings", nil, nil, nil, "{" + owner + "=X/" + owner + "}"}}),
	}
	for _, s := range steps {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
	}
	identInitialACL(t, p, b, b.DB, true)
}
