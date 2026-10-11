package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func rangeAggregateScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func rangeAggregateStep(pg bool, q string, columns, families []string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, columns, append([]string(nil), families...), rows, args...)
	s.RawTypes = append([]string(nil), families...)
	for i, family := range families {
		if pg && strings.Contains(family, "MULTIRANGE") {
			s.Types[i] = ""
		}
	}
	s.CheckRaw = func(t *testing.T, col, row int, value any) bool {
		family := families[col]
		if !strings.Contains(family, "RANGE") {
			return false
		}
		if value == nil {
			return true
		}
		if pg {
			if _, ok := value.([]byte); !ok {
				t.Fatalf("%s raw %T, want []byte", family, value)
			}
		} else if _, ok := value.(string); !ok {
			t.Fatalf("%s raw %T, want string", family, value)
		}
		return true
	}
	return s
}

func rangeAggregateRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func rangeAggregateMetadata(t *testing.T, rows *sql.Rows, pg bool, families []string) {
	t.Helper()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != len(families) {
		t.Fatalf("metadata count %d want %d", len(cols), len(families))
	}
	for i, c := range cols {
		name, scan := families[i], reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
			if strings.Contains(name, "MULTIRANGE") {
				name = ""
			}
		}
		if c.DatabaseTypeName() != name || c.ScanType() != scan {
			t.Fatalf("column %d name=%q scan=%v want %q/%v", i, c.DatabaseTypeName(), c.ScanType(), name, scan)
		}
		if n, ok := c.Length(); ok {
			t.Fatalf("unexpected length %d", n)
		}
		if precision, scale, ok := c.DecimalSize(); ok {
			t.Fatalf("unexpected decimal %d/%d", precision, scale)
		}
		nullable, known := c.Nullable()
		if known != !pg || nullable != !pg {
			t.Fatalf("nullable %v/%v want %v/%v", nullable, known, !pg, !pg)
		}
	}
}
