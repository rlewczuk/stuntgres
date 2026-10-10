package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func pgCastMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, pgCastColumns) {
		t.Fatal("cast columns", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 6 {
		t.Fatal("cast types", types, err)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if pg {
			// Pinned pq v1.12.3 fieldDesc.Type has no OID or internal
			// "char" case; both expose interface{} metadata, independently
			// of their raw decimal-byte / string value codecs.
			scan = reflect.TypeFor[any]()
		} else if i >= 4 {
			scan = reflect.TypeFor[string]()
		}
		if typ.Name() != pgCastColumns[i] || typ.DatabaseTypeName() != pgCastTypes[i] || typ.ScanType() != scan {
			t.Fatalf("cast descriptor %d: name=%q type=%q ScanType=%v; want name=%q type=%q ScanType=%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), pgCastColumns[i], pgCastTypes[i], scan)
		}
		if nullable, known := typ.Nullable(); nullable || known == pg {
			t.Fatal("cast NOT NULL / pq unknown", i, nullable, known)
		}
		if length, known := typ.Length(); length != 0 || known {
			t.Fatal("cast length", i, length, known)
		}
		if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("cast decimal size", i, precision, scale, known)
		}
	}
}

func pgCastShape(t *testing.T, p *referencePair, b referenceBackend) {
	t.Helper()
	for _, query := range []string{
		"SELECT * FROM pg_catalog.pg_cast WHERE FALSE",
		"SELECT * FROM pg_cast WHERE FALSE",
		"SELECT * FROM pg_catalog.pg_cast WHERE castsource=4294967295::oid",
	} {
		s := referenceSelect(query, pgCastColumns, pgCastTypes, [][]any{})
		s.checkMetadata = func(t *testing.T, rows *sql.Rows) { pgCastMetadata(t, rows, b.pg) }
		pgDescriptionRows(t, p, b, s)
		pgDescriptionRows(t, p, b, referencePrepared(s))
	}
	s := referenceSelect("SELECT oid,relname,relnamespace,relkind,relnatts FROM pg_class WHERE oid=2605",
		[]string{"oid", "relname", "relnamespace", "relkind", "relnatts"}, []string{"OID", "NAME", "OID", "CHAR", "INT2"},
		[][]any{{int64(2605), "pg_cast", int64(11), "r", int64(6)}})
	pgDescriptionRows(t, p, b, s)
	pgDescriptionRows(t, p, b, referencePrepared(s))
	s = referenceSelect("SELECT c.castsource,c.casttarget,k.relname FROM pg_cast c JOIN pg_class k ON k.oid=2605 WHERE c.castsource=23 AND c.casttarget=20",
		[]string{"castsource", "casttarget", "relname"}, []string{"OID", "OID", "NAME"}, [][]any{{int64(23), int64(20), "pg_cast"}})
	pgDescriptionRows(t, p, b, s)
	pgDescriptionRows(t, p, b, referencePrepared(s))
}

func TestReferencePGCastShape(t *testing.T) {
	roleReferenceCleanup(t, []string{"pgcast_shape_reader"})
	pgCastScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		pgCastShape(t, p, b)
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE VIEW pgcast_shape_view AS SELECT castsource,casttarget FROM pg_catalog.pg_cast WHERE castsource=23 AND casttarget=20", 0))
		s := referenceSelect("SELECT * FROM pgcast_shape_view", []string{"castsource", "casttarget"}, []string{"OID", "OID"}, [][]any{{int64(23), int64(20)}})
		pgDescriptionRows(t, p, b, s)
		pgDescriptionRows(t, p, b, referencePrepared(s))
		owner, reader := referenceLockConnections(t, p.ctx, b.db)
		referenceRunStep(t, p.ctx, owner, b.recorder, referenceExec("CREATE ROLE pgcast_shape_reader", 0))
		referenceRunStep(t, p.ctx, reader, b.recorder, referenceExec("SET ROLE pgcast_shape_reader", 0))
		defer reader.ExecContext(p.ctx, "RESET ROLE")
		s = referenceSelect("SELECT castsource,casttarget FROM pg_catalog.pg_cast WHERE castsource=23 AND casttarget=20", []string{"castsource", "casttarget"}, []string{"OID", "OID"}, [][]any{{int64(23), int64(20)}})
		pgDescriptionExecutorRows(t, p, b, reader, s)
		pgDescriptionExecutorRows(t, p, b, reader, referencePrepared(s))
	})
}
