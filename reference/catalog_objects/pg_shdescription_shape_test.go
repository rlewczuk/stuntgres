package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

var pgShDescriptionTypes = []string{"OID", "OID", "TEXT"}

func pgShDescriptionSelect(query string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, fixture.PGShDescriptionColumns, pgShDescriptionTypes, rows, args...)
	s.Bag = true
	return s
}

func pgShDescriptionMetadata(t *testing.T, rows *sql.Rows, pg bool) []*sql.ColumnType {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, fixture.PGShDescriptionColumns) {
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
		if typ.Name() != fixture.PGShDescriptionColumns[i] || typ.DatabaseTypeName() != pgShDescriptionTypes[i] || typ.ScanType() != scan {
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

func pgShDescriptionCheck(t *testing.T, p *fixture.Pair, backend fixture.Backend, executor fixture.Executor, id int64, text *string) {
	t.Helper()
	rows := [][]any{}
	if text != nil {
		rows = [][]any{{id, int64(1260), *text}}
	}
	s := pgShDescriptionSelect("SELECT * FROM pg_catalog.pg_shdescription WHERE objoid=$1 AND classoid=1260", rows, id)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgShDescriptionMetadata(t, rows, backend.PG) }
	fixture.PgDescriptionExecutorRows(t, p, backend, executor, s)
	fixture.PgDescriptionExecutorRows(t, p, backend, executor, fixture.Prepared(s))
}

func TestReferencePGShDescriptionShape(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgsd_shape")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			for _, q := range []string{"SELECT * FROM pg_catalog.pg_shdescription WHERE FALSE", "SELECT * FROM pg_shdescription WHERE FALSE"} {
				for _, prepared := range []bool{false, true} {
					s := pgShDescriptionSelect(q, [][]any{})
					s.Prepared = prepared
					var saved []*sql.ColumnType
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { saved = pgShDescriptionMetadata(t, rows, backend.PG) }
					fixture.PgDescriptionRows(t, p, backend, s)
					pgShDescriptionSavedMetadata(t, saved, backend.PG)
				}
			}
			identity := fixture.Select("SELECT oid,relname,relnamespace,relkind,relnatts FROM pg_catalog.pg_class WHERE oid=2396", []string{"oid", "relname", "relnamespace", "relkind", "relnatts"}, []string{"OID", "NAME", "OID", "CHAR", "INT2"}, [][]any{{int64(2396), "pg_shdescription", int64(11), "r", int64(3)}})
			fixture.PgDescriptionRows(t, p, backend, identity)
			fixture.PgDescriptionRows(t, p, backend, fixture.Prepared(identity))
			for _, q := range []string{"CREATE SCHEMA pgsd_shape", "CREATE TABLE pgsd_shape.pg_shdescription(marker TEXT)", "INSERT INTO pgsd_shape.pg_shdescription VALUES('shadow')", "SET search_path TO pgsd_shape,pg_catalog"} {
				count := int64(0)
				if q == "INSERT INTO pgsd_shape.pg_shdescription VALUES('shadow')" {
					count = 1
				}
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(q, count))
			}
			shadow := fixture.Select("SELECT * FROM pg_shdescription", []string{"marker"}, []string{"TEXT"}, [][]any{{"shadow"}})
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, shadow)
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(shadow))
			fixture.PgDescriptionRows(t, p, backend, pgShDescriptionSelect("SELECT * FROM pg_catalog.pg_shdescription WHERE FALSE", [][]any{}))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("RESET search_path", 0))
		})
	}
}
