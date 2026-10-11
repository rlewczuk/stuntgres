package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func pgDependMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, fixture.PgDependColumns) {
		t.Fatal(cols, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 7 {
		t.Fatal(types, err)
	}
	for i, typ := range types {
		if typ.Name() != fixture.PgDependColumns[i] || typ.DatabaseTypeName() != fixture.PgDependTypes[i] {
			t.Fatal("descriptor", i, typ)
		}
		scan := reflect.TypeFor[int64]()
		if pg && (i == 6 || fixture.PgDependTypes[i] == "OID") {
			scan = reflect.TypeFor[any]()
		} else if i == 6 {
			scan = reflect.TypeFor[string]()
		} else if pg {
			scan = reflect.TypeFor[int32]()
		}
		if typ.ScanType() != scan {
			t.Fatal("scan type", i, typ.ScanType(), scan)
		}
		if length, known := typ.Length(); length != 0 || known {
			t.Fatal("non-varying length", i, length, known)
		}
		nullable, known := typ.Nullable()
		if nullable || known == pg {
			t.Fatal("NOT NULL", i, nullable, known)
		}
		if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("precision", i)
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for i, typ := range types {
		if typ.DatabaseTypeName() != fixture.PgDependTypes[i] {
			t.Fatal("lost saved metadata")
		}
	}
}
func TestReferencePGDependShape(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
		identity := fixture.Select("SELECT oid,relname,relnamespace,relkind,relnatts FROM pg_catalog.pg_class WHERE oid=2608", []string{"oid", "relname", "relnamespace", "relkind", "relnatts"}, []string{"OID", "NAME", "OID", "CHAR", "INT2"}, [][]any{{int64(2608), "pg_depend", int64(11), "r", int64(7)}})
		fixture.PgDescriptionExecutorRows(t, p, b, b.DB, identity)
		fixture.PgDescriptionExecutorRows(t, p, b, b.DB, fixture.Prepared(identity))
		for _, q := range []string{"SELECT * FROM pg_catalog.pg_depend WHERE FALSE", "SELECT * FROM pg_catalog.pg_depend WHERE objid=4294967295::oid", "SELECT * FROM pg_catalog.pg_depend WHERE objid=$1"} {
			args := []any{}
			if q == "SELECT * FROM pg_catalog.pg_depend WHERE objid=$1" {
				args = []any{nil}
			}
			s := fixture.PgDependSelect(q, [][]any{}, args...)
			// Inspect descriptors on a separate cursor so metadata closure does not
			// consume the shared fixture's independently recorded stream.
			rows, err := b.DB.QueryContext(p.Ctx, q, args...)
			if err != nil {
				t.Fatal(err)
			}
			pgDependMetadata(t, rows, b.PG)
			fixture.PgDescriptionExecutorRows(t, p, b, b.DB, s)
			fixture.PgDescriptionExecutorRows(t, p, b, b.DB, fixture.Prepared(s))
		}
	})
}
func TestReferencePGDependQueryRoutes(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgdep_routes")
	p := fixture.NewScenario(t)
	fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
		fixture.PgDependExec(t, p, b, "CREATE SCHEMA pgdep_routes", "CREATE TABLE pgdep_routes.route(a INT)")
		id := fixture.PgDependRelation(t, b, "pgdep_routes.route")
		ns := fixture.PgDependNamespace(t, b, id)
		s := fixture.PgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1", [][]any{fixture.PgDependEdge(1259, id, 0, 2615, ns, 0, "n")}, id)
		for _, prepared := range []bool{false, true} {
			for _, plain := range []bool{false, true} {
				s.Prepared = prepared
				s.NonContext = plain
				fixture.PgDescriptionExecutorRows(t, p, b, b.DB, s)
			}
		}
		s.Prepared = false
		s.NonContext = false
		stmt, err := b.DB.PrepareContext(p.Ctx, s.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		fixture.PgDescriptionExecutorRows(t, p, b, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: s.Query}, s)
		plainRows, err := stmt.Query(id)
		if err != nil {
			t.Fatal(err)
		}
		fixture.PgDependRetained(t, plainRows, [][]any{fixture.PgDependEdge(1259, id, 0, 2615, ns, 0, "n")})
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT d.deptype,count(*) FROM pg_catalog.pg_depend d JOIN pg_catalog.pg_class c ON d.objid=c.oid WHERE d.classid=1259 AND c.oid=$1 GROUP BY d.deptype ORDER BY d.deptype", []string{"deptype", "count"}, []string{"CHAR", "INT8"}, [][]any{{"n", int64(1)}}, id))
		fixture.PgDependExec(t, p, b, "CREATE TABLE pgdep_routes.pg_depend(marker TEXT)", "SET search_path TO pgdep_routes,pg_catalog")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT * FROM pg_depend", []string{"marker"}, []string{"TEXT"}, [][]any{}))
		fixture.PgDescriptionExecutorRows(t, p, b, b.DB, fixture.PgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE FALSE", [][]any{}))
	})
}
