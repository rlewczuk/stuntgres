package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"strconv"
	"testing"
)

var pgDescriptionColumns = []string{"objoid", "classoid", "objsubid", "description"}
var pgDescriptionTypes = []string{"OID", "OID", "INT4", "TEXT"}

func pgDescriptionOIDText(oid int64) string { return strconv.FormatInt(oid, 10) }

// The expectation is numeric, independent of either driver's OID encoding.
// Preserve the fixture's raw codec and middleware assertions before comparing.
func pgDescriptionRows(t *testing.T, p *referencePair, backend referenceBackend, s referenceStep) [][]any {
	t.Helper()
	return pgDescriptionExecutorRows(t, p, backend, backend.db, s)
}

func pgDescriptionExecutorRows(t *testing.T, p *referencePair, backend referenceBackend, executor referenceExecutor, s referenceStep) [][]any {
	t.Helper()
	want := make([][]any, len(s.rows))
	for i, row := range s.rows {
		want[i] = append([]any(nil), row...)
		for j, v := range row {
			if s.types[j] == "OID" && v != nil && backend.pg {
				want[i][j] = strconv.FormatInt(v.(int64), 10)
			}
		}
	}
	s.rows = want
	s.checkScan = func(t *testing.T, rows *sql.Rows, index int) {
		cells, dest := make([]any, len(s.types)), make([]any, len(s.types))
		for i := range dest {
			dest[i] = &cells[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, v := range cells {
			oidReferenceValue(t, backend.pg, s.types[i], v)
		}
	}
	observed := referenceRunStep(t, p.ctx, executor, backend.recorder, s)
	for _, row := range observed.rows {
		for i, v := range row {
			if backend.pg && s.types[i] == "OID" && v != nil {
				n, err := strconv.ParseUint(v.(string), 10, 32)
				if err != nil {
					t.Fatal(err)
				}
				row[i] = int64(n)
			}
		}
	}
	return observed.rows
}

func pgDescriptionMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, pgDescriptionColumns) {
		t.Fatal("description columns", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 4 {
		t.Fatal("description types", types, err)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if i == 3 {
			scan = reflect.TypeFor[string]()
		} else if pg && i < 2 {
			scan = reflect.TypeFor[any]()
		} else if pg {
			scan = reflect.TypeFor[int32]()
		}
		if typ.Name() != pgDescriptionColumns[i] || typ.DatabaseTypeName() != pgDescriptionTypes[i] || typ.ScanType() != scan {
			t.Fatal("description descriptor", i, typ)
		}
		nullable, known := typ.Nullable()
		if nullable || known == pg {
			t.Fatal("description NOT NULL / pq unknown", i, nullable, known)
		}
		length, lengthKnown := typ.Length()
		if i == 3 {
			if length != math.MaxInt64 || !lengthKnown {
				t.Fatal("description text length", length, lengthKnown)
			}
		} else if length != 0 || lengthKnown {
			t.Fatal("description numeric length", length, lengthKnown)
		}
		if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("description precision", precision, scale, known)
		}
	}
}

func pgDescriptionSelect(query string, rows [][]any, args ...any) referenceStep {
	s := referenceSelect(query, pgDescriptionColumns, pgDescriptionTypes, rows, args...)
	s.bag = true
	return s
}

func TestReferencePGDescriptionShape(t *testing.T) {
	schemaReferenceCleanup(t, "pgd_shape")
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			for _, query := range []string{
				"SELECT * FROM pg_catalog.pg_description WHERE FALSE",
				"SELECT * FROM pg_catalog.pg_description WHERE objoid=4294967295::oid",
				"SELECT * FROM pg_catalog.pg_description WHERE objoid=11 AND classoid=1259",
				"SELECT * FROM pg_catalog.pg_description WHERE objoid=11 AND classoid=2615 AND objsubid=1",
				"SELECT * FROM pg_catalog.pg_description WHERE objsubid=-1",
			} {
				s := pgDescriptionSelect(query, [][]any{})
				s.checkMetadata = func(t *testing.T, rows *sql.Rows) { pgDescriptionMetadata(t, rows, backend.pg) }
				pgDescriptionRows(t, p, backend, s)
				pgDescriptionRows(t, p, backend, referencePrepared(s))
			}
			identity := referenceSelect("SELECT oid,relname,relnamespace,relkind,relnatts FROM pg_catalog.pg_class WHERE oid=2609", []string{"oid", "relname", "relnamespace", "relkind", "relnatts"}, []string{"OID", "NAME", "OID", "CHAR", "INT2"}, [][]any{{int64(2609), "pg_description", int64(11), "r", int64(4)}})
			pgDescriptionRows(t, p, backend, identity)
			pgDescriptionRows(t, p, backend, referencePrepared(identity))
			for _, query := range []string{"CREATE SCHEMA pgd_shape", "CREATE TABLE pgd_shape.pg_description(marker TEXT)", "INSERT INTO pgd_shape.pg_description VALUES('shadow')", "SET search_path TO pgd_shape,pg_catalog"} {
				count := int64(0)
				if query == "INSERT INTO pgd_shape.pg_description VALUES('shadow')" {
					count = 1
				}
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec(query, count))
			}
			shadow := referenceSelect("SELECT * FROM pg_description", []string{"marker"}, []string{"TEXT"}, [][]any{{"shadow"}})
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, shadow)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(shadow))
			s := pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE FALSE", [][]any{})
			s.checkMetadata = func(t *testing.T, rows *sql.Rows) { pgDescriptionMetadata(t, rows, backend.pg) }
			pgDescriptionRows(t, p, backend, s)
			pgDescriptionRows(t, p, backend, referencePrepared(s))
		})
	}
}
