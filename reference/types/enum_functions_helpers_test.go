package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func enumFunctionsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			enumFunctionsSetup(t, p, backend, conn)
			body(t, p, backend, conn)
		})
	}
}

func enumFunctionsSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE TYPE mood AS ENUM ('z','a','m')",
		"CREATE TYPE other_mood AS ENUM ('z','a','m')",
		"CREATE TYPE empty_enum AS ENUM ()",
		"CREATE DOMAIN mood_domain AS mood",
		"CREATE TABLE enum_endpoints(id integer,lo mood,hi mood,other other_mood)",
	} {
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("INSERT INTO enum_endpoints VALUES (1,'z','a','a'),(2,'a','m','m'),(3,NULL,'a',NULL),(4,'a',NULL,NULL),(5,NULL,NULL,NULL)", 5))
}

// pq leaves dynamic enum OIDs unnamed; native computed arrays expose their
// existing associated array name. Scalar enum results are unnamed in both.
func enumFunctionsTypeName(pg bool, nativeName string) string {
	if pg {
		return ""
	}
	return nativeName
}

// Scalar and computed-array function results have dynamic enum OIDs.
func enumFunctionsMetadata(pg, array bool) func(*testing.T, *sql.Rows) {
	return enumFunctionsMetadataNullable(pg, array, false)
}

func enumFunctionsMetadataNullable(pg, array, declared bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(types) != 1 {
			t.Fatal("expected one enum function column", len(types))
		}
		c := types[0]
		length, lengthOK := c.Length()
		precision, scale, decimalOK := c.DecimalSize()
		nullable, nullableOK := c.Nullable()
		scan, name := reflect.TypeFor[string](), ""
		if array {
			name = enumFunctionsTypeName(pg, "_MOOD")
		}
		if pg {
			scan = reflect.TypeFor[any]()
		}
		wantNullable := declared && !pg
		if c.Name() != "v" || c.DatabaseTypeName() != name || c.ScanType() != scan || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK || nullable != wantNullable || nullableOK != wantNullable {
			t.Fatalf("enum function metadata got name=%q type=%q scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t; want v/%q/%v/0,false/0,0,false/%t,%t", c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK, name, scan, wantNullable, wantNullable)
		}
	}
}

func enumFunctionsSelect(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, query, want string, array, prepared bool, args ...any) {
	t.Helper()
	name := ""
	if array {
		name = enumFunctionsTypeName(b.PG, "_MOOD")
	}
	s := fixture.Select(query, []string{"v"}, []string{name}, [][]any{{want}}, args...)
	s.RawTypes = []string{"_ENUM"}
	s.Prepared = prepared
	s.CheckMetadata = enumFunctionsMetadata(b.PG, array)
	fixture.EnumReferenceRunStep(t, p.Ctx, db, b.Recorder, s)
}
