package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func circleScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func circleLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func circleDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic(code, message)
}

func circleInputDiagnostic(input string) fixture.Diagnostic {
	return circleDiagnostic("22P02", `invalid input syntax for type circle: "`+input+`"`)
}

func circleSelect(b fixture.Backend, q string, names, types []string, values [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, names, types, values, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if types[column] != "CIRCLE" {
			return false
		}
		if value == nil {
			return true
		}
		if b.PG {
			if _, ok := value.([]byte); !ok {
				t.Fatalf("circle PG raw %T, want []byte", value)
			}
		} else if _, ok := value.(string); !ok {
			t.Fatalf("circle native raw %T, want string", value)
		}
		return true
	}
	return s
}

func circleMetadata(t *testing.T, pg bool, rows *sql.Rows, nullable *bool) []*sql.ColumnType {
	t.Helper()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cols {
		if c.DatabaseTypeName() != "CIRCLE" {
			continue
		}
		wantScan := reflect.TypeFor[string]()
		if pg {
			wantScan = reflect.TypeFor[any]()
		}
		if c.ScanType() != wantScan {
			t.Errorf("circle ScanType %v want %v", c.ScanType(), wantScan)
		}
		if n, known := c.Nullable(); pg || nullable == nil {
			if n || known {
				t.Errorf("nullable %v/%v want false/false", n, known)
			}
		} else if !known || n != *nullable {
			t.Errorf("nullable %v/%v want %v/true", n, known, *nullable)
		}
		if n, known := c.Length(); n != 0 || known {
			t.Errorf("length %d/%v", n, known)
		}
		if p, s, known := c.DecimalSize(); p != 0 || s != 0 || known {
			t.Errorf("decimal %d/%d/%v", p, s, known)
		}
	}
	return cols
}
