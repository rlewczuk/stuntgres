package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func geometryTransformScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	geometryRelationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		body(t, p, b, c)
	})
}

func geometryTransformSelect(b fixture.Backend, q string, names, types []string, values [][]any, args ...any) fixture.Step {
	s := boxSelect(b, q, names, types, values, args...)
	baseRaw, baseMetadata := s.CheckRaw, s.CheckMetadata
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if types[column] != "PATH" && types[column] != "CIRCLE" {
			return baseRaw(t, column, row, value)
		}
		if value != nil {
			want := reflect.TypeFor[string]()
			if b.PG {
				want = reflect.TypeFor[[]byte]()
			}
			if reflect.TypeOf(value) != want {
				t.Fatalf("raw %s is %T want %v", types[column], value, want)
			}
			if v, ok := value.([]byte); ok && v == nil {
				t.Fatal("typed nil geometry payload")
			}
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		baseMetadata(t, rows)
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, c := range cols {
			if types[i] != "PATH" && types[i] != "CIRCLE" {
				continue
			}
			want := reflect.TypeFor[string]()
			if b.PG {
				want = reflect.TypeFor[any]()
			}
			if c.ScanType() != want {
				t.Fatalf("%s ScanType %v want %v", types[i], c.ScanType(), want)
			}
			if n, ok := c.Length(); n != 0 || ok {
				t.Fatalf("geometry length %d/%t", n, ok)
			}
			if p, s, ok := c.DecimalSize(); p != 0 || s != 0 || ok {
				t.Fatalf("geometry decimal %d,%d/%t", p, s, ok)
			}
		}
	}
	return s
}
