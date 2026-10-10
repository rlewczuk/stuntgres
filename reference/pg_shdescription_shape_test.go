package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
)

var pgShDescriptionColumns = []string{"objoid", "classoid", "description"}
var pgShDescriptionTypes = []string{"OID", "OID", "TEXT"}

func pgShDescriptionSelect(query string, rows [][]any, args ...any) referenceStep {
	s := referenceSelect(query, pgShDescriptionColumns, pgShDescriptionTypes, rows, args...)
	s.bag = true
	return s
}

func pgShDescriptionMetadata(t *testing.T, rows *sql.Rows, pg bool) []*sql.ColumnType {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, pgShDescriptionColumns) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 3 {
		t.Fatal(types, err)
	}
	pgShDescriptionSavedMetadata(t, types, pg)
	return types
}

func pgShDescriptionSavedMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if i == 2 {
			scan = reflect.TypeFor[string]()
		} else if pg {
			scan = reflect.TypeFor[any]()
		}
		if typ.Name() != pgShDescriptionColumns[i] || typ.DatabaseTypeName() != pgShDescriptionTypes[i] || typ.ScanType() != scan {
			t.Fatal("shared descriptor", i, typ)
		}
		if nullable, known := typ.Nullable(); nullable || known == pg {
			t.Fatal("NOT NULL / pq unknown", i, nullable, known)
		}
		length, known := typ.Length()
		if i == 2 {
			if length != math.MaxInt64 || !known {
				t.Fatal("TEXT length", length, known)
			}
		} else if length != 0 || known {
			t.Fatal("OID length", length, known)
		}
		if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("precision", precision, scale, known)
		}
	}
}

func pgShDescriptionCheck(t *testing.T, p *referencePair, backend referenceBackend, executor referenceExecutor, id int64, text *string) {
	t.Helper()
	rows := [][]any{}
	if text != nil {
		rows = [][]any{{id, int64(1260), *text}}
	}
	s := pgShDescriptionSelect("SELECT * FROM pg_catalog.pg_shdescription WHERE objoid=$1 AND classoid=1260", rows, id)
	s.checkMetadata = func(t *testing.T, rows *sql.Rows) { pgShDescriptionMetadata(t, rows, backend.pg) }
	pgDescriptionExecutorRows(t, p, backend, executor, s)
	pgDescriptionExecutorRows(t, p, backend, executor, referencePrepared(s))
}

// Identity comes from a separately owned relation, never the catalog under test.
func pgShDescriptionCreateRole(t *testing.T, p *referencePair, backend referenceBackend, name, schema string) int64 {
	t.Helper()
	for _, q := range []string{"CREATE ROLE " + name, "CREATE SEQUENCE " + schema + "." + name + "_identity", "ALTER SEQUENCE " + schema + "." + name + "_identity OWNER TO " + name} {
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec(q, 0))
	}
	var id int64
	if err := backend.db.QueryRowContext(p.ctx, "SELECT relowner FROM pg_catalog.pg_class WHERE relname=$1", name+"_identity").Scan(&id); err != nil || id <= 0 || id > math.MaxUint32 {
		t.Fatal("independent role identity", id, err)
	}
	referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("DROP SEQUENCE "+schema+"."+name+"_identity", 0))
	return id
}

func TestReferencePGShDescriptionShape(t *testing.T) {
	schemaReferenceCleanup(t, "pgsd_shape")
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			for _, q := range []string{"SELECT * FROM pg_catalog.pg_shdescription WHERE FALSE", "SELECT * FROM pg_shdescription WHERE FALSE"} {
				for _, prepared := range []bool{false, true} {
					s := pgShDescriptionSelect(q, [][]any{})
					s.prepared = prepared
					var saved []*sql.ColumnType
					s.checkMetadata = func(t *testing.T, rows *sql.Rows) { saved = pgShDescriptionMetadata(t, rows, backend.pg) }
					pgDescriptionRows(t, p, backend, s)
					pgShDescriptionSavedMetadata(t, saved, backend.pg)
				}
			}
			identity := referenceSelect("SELECT oid,relname,relnamespace,relkind,relnatts FROM pg_catalog.pg_class WHERE oid=2396", []string{"oid", "relname", "relnamespace", "relkind", "relnatts"}, []string{"OID", "NAME", "OID", "CHAR", "INT2"}, [][]any{{int64(2396), "pg_shdescription", int64(11), "r", int64(3)}})
			pgDescriptionRows(t, p, backend, identity)
			pgDescriptionRows(t, p, backend, referencePrepared(identity))
			for _, q := range []string{"CREATE SCHEMA pgsd_shape", "CREATE TABLE pgsd_shape.pg_shdescription(marker TEXT)", "INSERT INTO pgsd_shape.pg_shdescription VALUES('shadow')", "SET search_path TO pgsd_shape,pg_catalog"} {
				count := int64(0)
				if q == "INSERT INTO pgsd_shape.pg_shdescription VALUES('shadow')" {
					count = 1
				}
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec(q, count))
			}
			shadow := referenceSelect("SELECT * FROM pg_shdescription", []string{"marker"}, []string{"TEXT"}, [][]any{{"shadow"}})
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, shadow)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(shadow))
			pgDescriptionRows(t, p, backend, pgShDescriptionSelect("SELECT * FROM pg_catalog.pg_shdescription WHERE FALSE", [][]any{}))
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("RESET search_path", 0))
		})
	}
}
