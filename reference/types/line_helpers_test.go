package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func lineScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func lineMetadata(t *testing.T, pg bool, rows *sql.Rows, types []string, declared []bool) {
	t.Helper()
	fixture.BoxMetadata(t, pg, rows, types, declared)
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range cols {
		if types[i] != "LINE" {
			continue
		}
		want := reflect.TypeFor[string]()
		if pg {
			want = reflect.TypeFor[any]()
		}
		if c.ScanType() != want {
			t.Fatalf("LINE ScanType %v want %v", c.ScanType(), want)
		}
		if n, ok := c.Length(); n != 0 || ok {
			t.Fatalf("LINE Length %d/%t", n, ok)
		}
		if p, s, ok := c.DecimalSize(); p != 0 || s != 0 || ok {
			t.Fatalf("LINE DecimalSize %d,%d/%t", p, s, ok)
		}
	}
}

func lineSelect(b fixture.Backend, q string, columns, types []string, values [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, columns, types, values, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if types[column] != "LINE" {
			return false
		}
		if value != nil {
			want := reflect.TypeFor[string]()
			if b.PG {
				want = reflect.TypeFor[[]byte]()
			}
			if reflect.TypeOf(value) != want {
				t.Fatalf("raw LINE %d,%d %T want %v", row, column, value, want)
			}
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { lineMetadata(t, b.PG, rows, types, nil) }
	return s
}
