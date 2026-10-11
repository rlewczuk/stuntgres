package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

var pgDescriptionColumns = []string{"objoid", "classoid", "objsubid", "description"}
var pgDescriptionTypes = []string{"OID", "OID", "INT4", "TEXT"}

func pgDescriptionOIDText(oid int64) string { return strconv.FormatInt(oid, 10) }

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

func pgDescriptionSelect(query string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, pgDescriptionColumns, pgDescriptionTypes, rows, args...)
	s.Bag = true
	return s
}

func TestReferencePGDescriptionShape(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgd_shape")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			for _, query := range []string{
				"SELECT * FROM pg_catalog.pg_description WHERE FALSE",
				"SELECT * FROM pg_catalog.pg_description WHERE objoid=4294967295::oid",
				"SELECT * FROM pg_catalog.pg_description WHERE objoid=11 AND classoid=1259",
				"SELECT * FROM pg_catalog.pg_description WHERE objoid=11 AND classoid=2615 AND objsubid=1",
				"SELECT * FROM pg_catalog.pg_description WHERE objsubid=-1",
			} {
				s := pgDescriptionSelect(query, [][]any{})
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgDescriptionMetadata(t, rows, backend.PG) }
				fixture.PgDescriptionRows(t, p, backend, s)
				fixture.PgDescriptionRows(t, p, backend, fixture.Prepared(s))
			}
			identity := fixture.Select("SELECT oid,relname,relnamespace,relkind,relnatts FROM pg_catalog.pg_class WHERE oid=2609", []string{"oid", "relname", "relnamespace", "relkind", "relnatts"}, []string{"OID", "NAME", "OID", "CHAR", "INT2"}, [][]any{{int64(2609), "pg_description", int64(11), "r", int64(4)}})
			fixture.PgDescriptionRows(t, p, backend, identity)
			fixture.PgDescriptionRows(t, p, backend, fixture.Prepared(identity))
			for _, query := range []string{"CREATE SCHEMA pgd_shape", "CREATE TABLE pgd_shape.pg_description(marker TEXT)", "INSERT INTO pgd_shape.pg_description VALUES('shadow')", "SET search_path TO pgd_shape,pg_catalog"} {
				count := int64(0)
				if query == "INSERT INTO pgd_shape.pg_description VALUES('shadow')" {
					count = 1
				}
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(query, count))
			}
			shadow := fixture.Select("SELECT * FROM pg_description", []string{"marker"}, []string{"TEXT"}, [][]any{{"shadow"}})
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, shadow)
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(shadow))
			s := pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE FALSE", [][]any{})
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgDescriptionMetadata(t, rows, backend.PG) }
			fixture.PgDescriptionRows(t, p, backend, s)
			fixture.PgDescriptionRows(t, p, backend, fixture.Prepared(s))
		})
	}
}
