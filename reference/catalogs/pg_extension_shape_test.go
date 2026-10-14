package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

var pgExtensionColumns = []string{"oid", "extname", "extowner", "extnamespace", "extrelocatable", "extversion", "extconfig", "extcondition"}
var pgExtensionTypes = []string{"OID", "NAME", "OID", "OID", "BOOL", "TEXT", "_OID", "_TEXT"}

const pgExtensionProbe = "SELECT * FROM pg_catalog.pg_extension WHERE extname=$1"
const pgExtensionAbsent = "pgext_uninstalled_shape_scenario"

func pgExtensionScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	backends := p.Backends()
	// Exercise the actual release oracle first, without skipping native red.
	for i := len(backends) - 1; i >= 0; i-- {
		b := backends[i]
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func pgExtensionMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, pgExtensionColumns) {
		t.Fatal("extension columns", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	pgExtensionRetainedMetadata(t, types, pg)
}

func pgExtensionRetainedMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 8 {
		t.Fatal("extension column count", len(types))
	}
	for i, typ := range types {
		scan := reflect.TypeFor[string]()
		if i == 0 || i == 2 || i == 3 {
			scan = reflect.TypeFor[int64]()
		}
		if i == 4 {
			scan = reflect.TypeFor[bool]()
		}
		// pq fieldDesc.Type has no NAME/OID/array ScanType case.
		if pg && i != 4 && i != 5 {
			scan = reflect.TypeFor[any]()
		}
		if typ.Name() != pgExtensionColumns[i] || typ.DatabaseTypeName() != pgExtensionTypes[i] || typ.ScanType() != scan {
			t.Fatalf("extension descriptor %d: name=%q type=%q scan=%v; want %q %q %v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), pgExtensionColumns[i], pgExtensionTypes[i], scan)
		}
		nullable, known := typ.Nullable()
		if pg {
			if nullable || known {
				t.Fatal("pq unknown nullability", i, nullable, known)
			}
		} else if !known || nullable != (i >= 6) {
			t.Fatal("native nullability", i, nullable, known)
		}
		length, lengthKnown := typ.Length()
		wantLength, wantLengthKnown := int64(0), false
		// Root observed both drivers expose unbounded TEXT as MaxInt64.
		if i == 5 {
			wantLength, wantLengthKnown = 9223372036854775807, true
		}
		if length != wantLength || lengthKnown != wantLengthKnown {
			t.Fatal("extension length", i, length, lengthKnown)
		}
		if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("extension decimal", i, precision, scale, known)
		}
	}
}

func pgExtensionBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	fixture.PgDescriptionRows(t, p, b, s)
	fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
}

func pgExtensionEmpty(t *testing.T, p *fixture.Pair, b fixture.Backend, query string, args ...any) {
	t.Helper()
	s := fixture.Select(query, pgExtensionColumns, pgExtensionTypes, [][]any{}, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgExtensionMetadata(t, rows, b.PG) }
	pgExtensionBoth(t, p, b, s)
}

func pgExtensionClass(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	pgExtensionBoth(t, p, b, fixture.Select("SELECT oid,relname,relnamespace,relkind,relnatts FROM pg_class WHERE oid=3079", []string{"oid", "relname", "relnamespace", "relkind", "relnatts"}, []string{"OID", "NAME", "OID", "CHAR", "INT2"}, [][]any{{int64(3079), "pg_extension", int64(11), "r", int64(8)}}))
}

func TestReferencePGExtensionShape(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgext_shape_reader"})
	pgExtensionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, query := range []string{"SELECT * FROM pg_catalog.pg_extension WHERE FALSE", "SELECT * FROM pg_extension WHERE FALSE", "SELECT * FROM pg_catalog.pg_extension WHERE extname='" + pgExtensionAbsent + "'"} {
			pgExtensionEmpty(t, p, b, query)
		}
		pgExtensionClass(t, p, b)
		ownerName := "postgres"
		// The pinned fixture creates its bootstrap role10 as stuntgres.
		if b.PG {
			ownerName = "stuntgres"
		}
		pgExtensionBoth(t, p, b, fixture.Select("SELECT schemaname,tablename,tableowner,tablespace,hasindexes,hasrules,hastriggers,rowsecurity FROM pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_extension'",
			[]string{"schemaname", "tablename", "tableowner", "tablespace", "hasindexes", "hasrules", "hastriggers", "rowsecurity"}, []string{"NAME", "NAME", "NAME", "NAME", "BOOL", "BOOL", "BOOL", "BOOL"},
			[][]any{{"pg_catalog", "pg_extension", ownerName, nil, true, false, false, false}}))
		want := [][]any{
			{int64(1), "oid", int64(26), int64(-1), true, int64(0)},
			{int64(2), "extname", int64(19), int64(-1), true, int64(950)},
			{int64(3), "extowner", int64(26), int64(-1), true, int64(0)},
			{int64(4), "extnamespace", int64(26), int64(-1), true, int64(0)},
			{int64(5), "extrelocatable", int64(16), int64(-1), true, int64(0)},
			{int64(6), "extversion", int64(25), int64(-1), true, int64(950)},
			{int64(7), "extconfig", int64(1028), int64(-1), false, int64(0)},
			{int64(8), "extcondition", int64(1009), int64(-1), false, int64(950)},
		}
		pgExtensionBoth(t, p, b, fixture.Select("SELECT attnum,attname,atttypid,atttypmod,attnotnull,attcollation FROM pg_attribute WHERE attrelid=3079 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "atttypmod", "attnotnull", "attcollation"}, []string{"INT2", "NAME", "OID", "INT4", "BOOL", "OID"}, want))
		if b.PG {
			pgExtensionBoth(t, p, b, fixture.Select("SELECT t.typrelid,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid=3079", []string{"typrelid", "element_link", "array_link"}, []string{"OID", "BOOL", "BOOL"}, [][]any{{int64(3079), true, true}}))
		}
		owner, reader := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("CREATE ROLE pgext_shape_reader", 0))
		fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Exec("SET ROLE pgext_shape_reader", 0))
		defer reader.ExecContext(p.Ctx, "RESET ROLE")
		s := fixture.Select("SELECT * FROM pg_catalog.pg_extension WHERE FALSE", pgExtensionColumns, pgExtensionTypes, [][]any{})
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgExtensionMetadata(t, rows, b.PG) }
		fixture.PgDescriptionExecutorRows(t, p, b, reader, s)
		fixture.PgDescriptionExecutorRows(t, p, b, reader, fixture.Prepared(s))
		if b.PG {
			for _, query := range []string{"INSERT INTO pg_catalog.pg_extension VALUES(1,'pgext_rejected',10,11,false,'1.0',NULL,NULL)", "UPDATE pg_catalog.pg_extension SET extversion='x' WHERE FALSE", "DELETE FROM pg_catalog.pg_extension WHERE FALSE"} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_extension"})
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, reader, b.Recorder, s)
				}
			}
		}
	})
}
