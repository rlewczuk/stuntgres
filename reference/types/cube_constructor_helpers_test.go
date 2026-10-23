package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// The constructor definitions share one body across all available backends.
func cubeConstructorScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

// Independently specified cube text, never obtained from the native formatter.
func cubeConstructorArray(n int, element string) string {
	return "{" + strings.TrimSuffix(strings.Repeat(element+",", n), ",") + "}"
}

func cubeConstructorPoint(n int, element string) string {
	return "(" + strings.TrimSuffix(strings.Repeat(element+", ", n), ", ") + ")"
}

// Unlike cubeSelect this supports mixed cube/integer/bool projections without
// hiding the driver contracts of the ordinary scalar columns.
func cubeConstructorSelect(b fixture.Backend, q string, names, types []string, rows [][]any, args ...any) fixture.Step {
	dbTypes := append([]string(nil), types...)
	for i, typ := range types {
		if typ == "CUBE" && b.PG {
			dbTypes[i] = ""
		}
	}
	s := fixture.Select(q, names, dbTypes, rows, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if types[column] != "CUBE" {
			return false
		}
		if value == nil {
			return true // Complete row assertions independently check SQL NULL.
		}
		if b.PG {
			if _, ok := value.([]byte); !ok {
				t.Fatalf("cube PG raw cell %d/%d = %T, want []byte", row, column, value)
			}
		} else if _, ok := value.(string); !ok {
			t.Fatalf("cube native raw cell %d/%d = %T, want string", row, column, value)
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, cursor *sql.Rows) {
		columns, err := cursor.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, typ := range types {
			if typ != "CUBE" {
				continue
			}
			wantScan := reflect.TypeFor[string]()
			if b.PG {
				wantScan = reflect.TypeFor[any]()
			}
			if columns[i].ScanType() != wantScan {
				t.Fatalf("cube ScanType = %v want %v", columns[i].ScanType(), wantScan)
			}
			if n, known := columns[i].Length(); n != 0 || known {
				t.Fatalf("cube length = %d/%v", n, known)
			}
			if p, scale, known := columns[i].DecimalSize(); p != 0 || scale != 0 || known {
				t.Fatalf("cube precision = %d/%d/%v", p, scale, known)
			}
		}
	}
	return s
}

func cubeConstructorDiagnostic(code, message, detail string) fixture.Diagnostic {
	want := fixture.ControlsReferenceDiagnostic(code, message)
	want.Detail = detail
	return want
}

const cubeConstructorDimensionDetail = "A cube cannot have more than 100 dimensions."

func cubeConstructorMismatch() fixture.Diagnostic {
	return cubeConstructorDiagnostic("2202E", "UR and LL arrays must be of same length", "")
}

func cubeConstructorLimit(message string) fixture.Diagnostic {
	return cubeConstructorDiagnostic("54000", message, cubeConstructorDimensionDetail)
}

func cubeConstructorHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
}
