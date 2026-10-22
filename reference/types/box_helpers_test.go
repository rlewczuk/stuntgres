package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func boxScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func boxQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

type boxValuer struct {
	value driver.Value
	err   error
}

func (v boxValuer) Value() (driver.Value, error) { return v.value, v.err }

// The expected logical values are supplied independently by each scenario.
// Validate the public raw adapter contract before fixture normalization.
func boxSelect(b fixture.Backend, query string, columns, types []string, values [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, columns, types, values, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if types[column] != "BOX" && types[column] != "POINT" {
			return false
		}
		if value != nil {
			want := reflect.TypeFor[string]()
			if b.PG {
				want = reflect.TypeFor[[]byte]()
			}
			if reflect.TypeOf(value) != want {
				t.Fatalf("raw %s cell %d,%d is %T want %v", types[column], row, column, value, want)
			}
			if v, ok := value.([]byte); ok && v == nil {
				t.Fatal("typed nil bytes are not a non-NULL geometry value")
			}
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		boxMetadata(t, b.PG, rows, types, nil)
	}
	return s
}

// declared gives source nullability only where the query directly selects
// physical columns; expression nullability is deliberately not invented.
func boxMetadata(t *testing.T, pg bool, rows *sql.Rows, types []string, declared []bool) {
	t.Helper()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != len(types) {
		t.Fatalf("metadata width %d want %d", len(cols), len(types))
	}
	for i, c := range cols {
		if c.DatabaseTypeName() != types[i] {
			t.Fatalf("column %d type %s want %s", i, c.DatabaseTypeName(), types[i])
		}
		if types[i] == "BOX" || types[i] == "POINT" {
			want := reflect.TypeFor[string]()
			if pg {
				want = reflect.TypeFor[any]()
			}
			if c.ScanType() != want {
				t.Fatalf("column %d ScanType %v want %v", i, c.ScanType(), want)
			}
			if n, ok := c.Length(); n != 0 || ok {
				t.Fatalf("geometry length %d/%t", n, ok)
			}
			if p, s, ok := c.DecimalSize(); p != 0 || s != 0 || ok {
				t.Fatalf("geometry precision %d,%d/%t", p, s, ok)
			}
		}
		if declared != nil {
			n, ok := c.Nullable()
			known := !pg
			want := known && declared[i]
			if n != want || ok != known {
				t.Fatalf("column %d nullable %t/%t want %t/%t", i, n, ok, want, known)
			}
		}
	}
}

func boxError(query, code, message, position, hint string, prepared, runtime bool, args ...any) fixture.Step {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Position, d.Hint = position, hint
	s := fixture.ErrorDiagnostic(query, d, args...)
	s.Prepared = prepared
	s.SelectRows = strings.HasPrefix(query, "SELECT")
	s.ErrorStage = "execute"
	if prepared && !runtime {
		s.ErrorStage = "prepare"
	}
	return s
}
