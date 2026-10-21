package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

var pgTransformColumns = []string{"oid", "trftype", "trflang", "trffromsql", "trftosql"}
var pgTransformTypes = []string{"OID", "OID", "OID", "REGPROC", "REGPROC"}

const pgTransformOIDProbe = "SELECT * FROM pg_catalog.pg_transform WHERE oid=$1"
const pgTransformPairProbe = "SELECT * FROM pg_catalog.pg_transform WHERE trftype=$1 AND trflang=$2"

func pgTransformScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func pgTransformSavedMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 5 {
		t.Fatal("transform descriptor width", len(types))
	}
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if i >= 3 {
			scan = reflect.TypeFor[string]()
		}
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if typ.Name() != pgTransformColumns[i] || typ.DatabaseTypeName() != pgTransformTypes[i] || typ.ScanType() != scan {
			t.Fatalf("transform descriptor %d: %q/%q/%v, want %q/%q/%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), pgTransformColumns[i], pgTransformTypes[i], scan)
		}
		if nullable, known := typ.Nullable(); nullable || known != !pg {
			t.Fatal("transform nullability", i, nullable, known)
		}
		if length, known := typ.Length(); length != 0 || known {
			t.Fatal("transform length", i, length, known)
		}
		if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("transform decimal size", i, precision, scale, known)
		}
	}
}

func pgTransformMetadata(t *testing.T, rows *sql.Rows, pg bool) []*sql.ColumnType {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, pgTransformColumns) {
		t.Fatal("transform columns", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	pgTransformSavedMetadata(t, types, pg)
	return types
}

func pgTransformStep(b fixture.Backend, q string, args ...any) fixture.Step {
	s := fixture.Select(q, pgTransformColumns, pgTransformTypes, [][]any{}, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgTransformMetadata(t, rows, b.PG) }
	return s
}

func pgTransformBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.PgDescriptionExecutorRows(t, p, b, e, s)
	fixture.PgDescriptionExecutorRows(t, p, b, e, fixture.Prepared(s))
}

func TestReferencePGTransformInventory(t *testing.T) {
	pgTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, relation := range []string{"pg_catalog.pg_transform", "pg_transform"} {
			s := pgTransformStep(b, "SELECT * FROM "+relation)
			s.Bag = true
			pgTransformBoth(t, p, b, b.DB, s)
		}
		// Ordinary extension availability and SQL routines do not implement transforms.
		for _, q := range []string{
			"CREATE EXTENSION unaccent VERSION '1.1'",
			"CREATE FUNCTION public.ptr_identity(INT) RETURNS INT LANGUAGE SQL AS 'SELECT $1'",
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
		}
		pgTransformBoth(t, p, b, b.DB, pgTransformStep(b, "SELECT * FROM pg_catalog.pg_transform"))
		// Empty joins witness the callable identity paths, not populated FK fidelity.
		pgTransformBoth(t, p, b, b.DB, fixture.Select("SELECT t.oid,ty.oid AS type_oid,l.oid AS language_oid,f.oid AS from_oid,s.oid AS to_oid FROM pg_transform t JOIN pg_type ty ON ty.oid=t.trftype JOIN pg_language l ON l.oid=t.trflang LEFT JOIN pg_proc f ON f.oid=t.trffromsql LEFT JOIN pg_proc s ON s.oid=t.trftosql", []string{"oid", "type_oid", "language_oid", "from_oid", "to_oid"}, []string{"OID", "OID", "OID", "OID", "OID"}, [][]any{}))
	})
}

func TestReferencePGTransformShape(t *testing.T) {
	pgTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgTransformBoth(t, p, b, b.DB, pgTransformStep(b, "SELECT * FROM pg_transform WHERE FALSE"))
		rows, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_catalog.pg_transform WHERE FALSE")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		saved := pgTransformMetadata(t, rows, b.PG)
		if rows.Next() || rows.Err() != nil {
			t.Fatal("empty transform cursor", rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		pgTransformSavedMetadata(t, saved, b.PG)
	})
}

func TestReferencePGTransformCatalogLinks(t *testing.T) {
	pgTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		both := func(s fixture.Step) { pgTransformBoth(t, p, b, b.DB, s) }
		both(fixture.Select("SELECT oid,relname,relnamespace,relowner,relkind,relpersistence,relnatts,relhasindex FROM pg_class WHERE oid=3576", []string{"oid", "relname", "relnamespace", "relowner", "relkind", "relpersistence", "relnatts", "relhasindex"}, []string{"OID", "NAME", "OID", "OID", "CHAR", "CHAR", "INT2", "BOOL"}, [][]any{{int64(3576), "pg_transform", int64(11), int64(10), "r", "p", int64(5), true}}))
		both(fixture.Select("SELECT attnum,attname,atttypid,atttypmod,attnotnull,attcollation,attndims FROM pg_attribute WHERE attrelid=3576 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "atttypmod", "attnotnull", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "INT4", "BOOL", "OID", "INT2"}, [][]any{
			{int64(1), "oid", int64(26), int64(-1), true, int64(0), int64(0)},
			{int64(2), "trftype", int64(26), int64(-1), true, int64(0), int64(0)},
			{int64(3), "trflang", int64(26), int64(-1), true, int64(0), int64(0)},
			{int64(4), "trffromsql", int64(24), int64(-1), true, int64(0), int64(0)},
			{int64(5), "trftosql", int64(24), int64(-1), true, int64(0), int64(0)},
		}))
		both(fixture.Select("SELECT reltype='pg_catalog.pg_transform'::regtype::oid AS row_type_link FROM pg_class WHERE oid=3576", []string{"row_type_link"}, []string{"BOOL"}, [][]any{{true}}))
		both(fixture.Select("SELECT t.typrelid,t.typname,a.typname AS array_name,t.typnamespace,a.typnamespace AS array_namespace,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid=3576", []string{"typrelid", "typname", "array_name", "typnamespace", "array_namespace", "element_link", "array_link"}, []string{"OID", "NAME", "NAME", "OID", "OID", "BOOL", "BOOL"}, [][]any{{int64(3576), "pg_transform", "_pg_transform", int64(11), int64(11), true, true}}))
		owner := "postgres"
		if b.PG {
			owner = "stuntgres"
		}
		both(fixture.Select("SELECT schemaname,tablename,tableowner,tablespace,hasindexes,hasrules,hastriggers,rowsecurity FROM pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_transform'", []string{"schemaname", "tablename", "tableowner", "tablespace", "hasindexes", "hasrules", "hastriggers", "rowsecurity"}, []string{"NAME", "NAME", "NAME", "NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"pg_catalog", "pg_transform", owner, nil, true, false, false, false}}))
		both(fixture.Select("SELECT viewname FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_transform'", []string{"viewname"}, []string{"NAME"}, [][]any{}))
		links := fixture.Select("SELECT fktable::oid,fkcols,pktable::oid,pkcols,is_array,is_opt FROM pg_get_catalog_foreign_keys() WHERE fktable::oid=3576", []string{"fktable", "fkcols", "pktable", "pkcols", "is_array", "is_opt"}, []string{"OID", "_TEXT", "OID", "_TEXT", "BOOL", "BOOL"}, [][]any{
			{int64(3576), "{trftype}", int64(1247), "{oid}", false, false},
			{int64(3576), "{trflang}", int64(2612), "{oid}", false, false},
			{int64(3576), "{trffromsql}", int64(1255), "{oid}", false, true},
			{int64(3576), "{trftosql}", int64(1255), "{oid}", false, true},
		})
		links.Bag = true
		both(links)
	})
}
