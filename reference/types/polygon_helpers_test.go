package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const polygonTriangle = "((0,0),(4,0),(0,4))"
const polygonRotated = "((4,0),(0,4),(0,0))"

func polygonScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func polygonMetadata(t *testing.T, pg bool, rows *sql.Rows, types []string, declared []bool) {
	t.Helper()
	fixture.BoxMetadata(t, pg, rows, types, declared)
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range cols {
		if types[i] != "POLYGON" {
			continue
		}
		want := reflect.TypeFor[string]()
		if pg {
			want = reflect.TypeFor[any]()
		}
		if c.ScanType() != want {
			t.Fatalf("polygon ScanType %v want %v", c.ScanType(), want)
		}
		if n, ok := c.Length(); n != 0 || ok {
			t.Fatalf("polygon Length %d/%t", n, ok)
		}
		if p, s, ok := c.DecimalSize(); p != 0 || s != 0 || ok {
			t.Fatalf("polygon DecimalSize %d,%d/%t", p, s, ok)
		}
	}
}

func polygonSelect(b fixture.Backend, query string, columns, types []string, values [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, columns, types, values, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if types[column] != "POLYGON" {
			return false
		}
		if value != nil {
			want := reflect.TypeFor[string]()
			if b.PG {
				want = reflect.TypeFor[[]byte]()
			}
			if reflect.TypeOf(value) != want {
				t.Fatalf("raw polygon cell %d,%d: %T want %v", row, column, value, want)
			}
			if v, ok := value.([]byte); ok && v == nil {
				t.Fatal("non-NULL polygon has nil bytes")
			}
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) { polygonMetadata(t, b.PG, r, types, nil) }
	return s
}

func polygonInputDiagnostic(input string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type polygon: \""+input+"\"")
}
