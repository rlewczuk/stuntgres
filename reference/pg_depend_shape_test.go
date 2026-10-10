package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func pgDependMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, pgDependColumns) {
		t.Fatal(cols, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 7 {
		t.Fatal(types, err)
	}
	for i, typ := range types {
		if typ.Name() != pgDependColumns[i] || typ.DatabaseTypeName() != pgDependTypes[i] {
			t.Fatal("descriptor", i, typ)
		}
		scan := reflect.TypeFor[int64]()
		if pg && (i == 6 || pgDependTypes[i] == "OID") {
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
		if typ.DatabaseTypeName() != pgDependTypes[i] {
			t.Fatal("lost saved metadata")
		}
	}
}
func TestReferencePGDependShape(t *testing.T) {
	p := newReferenceScenario(t)
	pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
		identity := referenceSelect("SELECT oid,relname,relnamespace,relkind,relnatts FROM pg_catalog.pg_class WHERE oid=2608", []string{"oid", "relname", "relnamespace", "relkind", "relnatts"}, []string{"OID", "NAME", "OID", "CHAR", "INT2"}, [][]any{{int64(2608), "pg_depend", int64(11), "r", int64(7)}})
		pgDescriptionExecutorRows(t, p, b, b.db, identity)
		pgDescriptionExecutorRows(t, p, b, b.db, referencePrepared(identity))
		for _, q := range []string{"SELECT * FROM pg_catalog.pg_depend WHERE FALSE", "SELECT * FROM pg_catalog.pg_depend WHERE objid=4294967295::oid", "SELECT * FROM pg_catalog.pg_depend WHERE objid=$1"} {
			args := []any{}
			if q == "SELECT * FROM pg_catalog.pg_depend WHERE objid=$1" {
				args = []any{nil}
			}
			s := pgDependSelect(q, [][]any{}, args...)
			// Inspect descriptors on a separate cursor so metadata closure does not
			// consume the shared fixture's independently recorded stream.
			rows, err := b.db.QueryContext(p.ctx, q, args...)
			if err != nil {
				t.Fatal(err)
			}
			pgDependMetadata(t, rows, b.pg)
			pgDescriptionExecutorRows(t, p, b, b.db, s)
			pgDescriptionExecutorRows(t, p, b, b.db, referencePrepared(s))
		}
	})
}
func TestReferencePGDependQueryRoutes(t *testing.T) {
	schemaReferenceCleanup(t, "pgdep_routes")
	p := newReferenceScenario(t)
	pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
		pgDependExec(t, p, b, "CREATE SCHEMA pgdep_routes", "CREATE TABLE pgdep_routes.route(a INT)")
		id := pgDependRelation(t, b, "pgdep_routes.route")
		ns := pgDependNamespace(t, b, id)
		s := pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1", [][]any{pgDependEdge(1259, id, 0, 2615, ns, 0, "n")}, id)
		for _, prepared := range []bool{false, true} {
			for _, plain := range []bool{false, true} {
				s.prepared = prepared
				s.nonContext = plain
				pgDescriptionExecutorRows(t, p, b, b.db, s)
			}
		}
		s.prepared = false
		s.nonContext = false
		stmt, err := b.db.PrepareContext(p.ctx, s.query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		pgDescriptionExecutorRows(t, p, b, referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: s.query}, s)
		plainRows, err := stmt.Query(id)
		if err != nil {
			t.Fatal(err)
		}
		pgDependRetained(t, plainRows, [][]any{pgDependEdge(1259, id, 0, 2615, ns, 0, "n")})
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT d.deptype,count(*) FROM pg_catalog.pg_depend d JOIN pg_catalog.pg_class c ON d.objid=c.oid WHERE d.classid=1259 AND c.oid=$1 GROUP BY d.deptype ORDER BY d.deptype", []string{"deptype", "count"}, []string{"CHAR", "INT8"}, [][]any{{"n", int64(1)}}, id))
		pgDependExec(t, p, b, "CREATE TABLE pgdep_routes.pg_depend(marker TEXT)", "SET search_path TO pgdep_routes,pg_catalog")
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT * FROM pg_depend", []string{"marker"}, []string{"TEXT"}, [][]any{}))
		pgDescriptionExecutorRows(t, p, b, b.db, pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE FALSE", [][]any{}))
	})
}
