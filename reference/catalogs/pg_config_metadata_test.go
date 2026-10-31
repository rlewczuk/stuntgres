package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func pgConfigMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, pgConfigColumns) {
		t.Fatal("pg_config columns", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 2 {
		t.Fatal("pg_config descriptors", types, err)
	}
	for i, typ := range types {
		nullable, nullableKnown := typ.Nullable()
		length, lengthKnown := typ.Length()
		precision, scale, decimalKnown := typ.DecimalSize()
		if typ.Name() != pgConfigColumns[i] || typ.DatabaseTypeName() != "TEXT" {
			t.Error("pg_config TEXT descriptor", i, typ.Name(), typ.DatabaseTypeName())
		}
		if (pg && (nullable || nullableKnown)) || (!pg && (!nullable || !nullableKnown)) {
			t.Error("pg_config nullability", i, nullable, nullableKnown)
		}
		if length != math.MaxInt64 || !lengthKnown || typ.ScanType() != reflect.TypeOf("") {
			t.Error("TEXT metadata", i, length, lengthKnown, typ.ScanType())
		}
		if precision != 0 || scale != 0 || decimalKnown {
			t.Error("TEXT decimal metadata", i, precision, scale, decimalKnown)
		}
	}
}

func TestReferencePGConfigMetadata(t *testing.T) {
	pgConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, prepared := range []bool{false, true} {
			for _, populated := range []bool{false, true} {
				query := pgConfigSelect + " WHERE FALSE"
				var want [][]any
				if populated {
					query = pgConfigSelect + " WHERE name='VERSION'"
					want = pgConfigVersion(b)
				}
				s := pgConfigStep(query, want)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgConfigMetadata(t, rows, b.PG) }
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		owner := "postgres"
		if b.PG {
			owner = "stuntgres"
		}
		steps := []fixture.Step{
			fixture.Select("SELECT attnum::int4,attname::text,atttypid::bigint,atttypmod::int4,attndims::int4,attcollation::bigint,attnotnull,attisdropped FROM pg_attribute WHERE attrelid='pg_catalog.pg_config'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "atttypmod", "attndims", "attcollation", "attnotnull", "attisdropped"}, []string{"INT4", "TEXT", "INT8", "INT4", "INT4", "INT8", "BOOL", "BOOL"}, [][]any{{int64(1), "name", int64(25), int64(-1), int64(0), int64(100), false, false}, {int64(2), "setting", int64(25), int64(-1), int64(0), int64(100), false, false}}),
			fixture.Select("SELECT c.relkind::text,c.relnamespace::bigint,c.relnatts::int4,c.relhasindex,c.relowner::regrole::text,t.typname::text,t.typtype::text,t.typrelid=c.oid AS owns_relation,a.typelem=t.oid AS owns_element,t.typnamespace=c.relnamespace AS row_namespace,a.typnamespace=c.relnamespace AS array_namespace FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_config'::regclass", []string{"relkind", "relnamespace", "relnatts", "relhasindex", "relowner", "typname", "typtype", "owns_relation", "owns_element", "row_namespace", "array_namespace"}, []string{"TEXT", "INT8", "INT4", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(11), int64(2), false, owner, "pg_config", "c", true, true, true, true}}),
			fixture.Select("SELECT schemaname::text,viewname::text FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_config'", []string{"schemaname", "viewname"}, []string{"TEXT", "TEXT"}, [][]any{{"pg_catalog", "pg_config"}}),
			fixture.Select("SELECT pg_get_viewdef('pg_catalog.pg_config'::regclass,false) AS nonpretty,pg_get_viewdef('pg_catalog.pg_config'::regclass,true) AS pretty,definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_config'", []string{"nonpretty", "pretty", "definition"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{pgConfigViewDefinition, pgConfigViewDefinition, pgConfigViewDefinition}}),
		}
		for _, s := range steps {
			for _, prepared := range []bool{false, true} {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
	})
}
