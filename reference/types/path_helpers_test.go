package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func pathScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func pathMetadata(t *testing.T, pg bool, rows *sql.Rows, types []string, declared []bool) {
	t.Helper()
	boxMetadata(t, pg, rows, types, declared)
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range cols {
		if types[i] != "PATH" {
			continue
		}
		want := reflect.TypeFor[string]()
		if pg {
			want = reflect.TypeFor[any]()
		}
		if c.ScanType() != want {
			t.Fatalf("PATH ScanType %v want %v", c.ScanType(), want)
		}
		if n, ok := c.Length(); n != 0 || ok {
			t.Fatalf("PATH Length %d/%t", n, ok)
		}
		if p, s, ok := c.DecimalSize(); p != 0 || s != 0 || ok {
			t.Fatalf("PATH DecimalSize %d,%d/%t", p, s, ok)
		}
	}
}

func pathSelect(b fixture.Backend, q string, columns, types []string, values [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, columns, types, values, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if types[column] != "PATH" {
			return false
		}
		if value != nil {
			want := reflect.TypeFor[string]()
			if b.PG {
				want = reflect.TypeFor[[]byte]()
			}
			if reflect.TypeOf(value) != want {
				t.Fatalf("raw PATH %d,%d %T want %v", row, column, value, want)
			}
			if v, ok := value.([]byte); ok && v == nil {
				t.Fatal("typed nil bytes are not a non-NULL PATH")
			}
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pathMetadata(t, b.PG, rows, types, nil) }
	return s
}
