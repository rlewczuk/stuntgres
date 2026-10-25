package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func ean13Scenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func ean13Literal(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func ean13Diagnostic(code, message string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic(code, message)
}

func ean13Select(b fixture.Backend, q string, names, types []string, expected [][]any, nullable []bool, args ...any) fixture.Step {
	dbTypes := append([]string(nil), types...)
	for i, typ := range types {
		if (typ == "EAN13" || typ == "ISBN" || typ == "ISBN13" || typ == "ISMN" || typ == "ISMN13" || typ == "ISSN" || typ == "ISSN13") && b.PG {
			dbTypes[i] = ""
		}
	}
	s := fixture.Select(q, names, dbTypes, expected, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if types[column] != "EAN13" && types[column] != "ISBN" && types[column] != "ISBN13" && types[column] != "ISMN" && types[column] != "ISMN13" && types[column] != "ISSN" && types[column] != "ISSN13" {
			return false
		}
		if value != nil {
			want := reflect.TypeFor[string]()
			if b.PG {
				want = reflect.TypeFor[[]byte]()
			}
			if reflect.TypeOf(value) != want {
				t.Fatalf("%s raw %d/%d = %T, want %v", types[column], row, column, value, want)
			}
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		ean13ColumnMetadata(t, b.PG, columns, names, types, nullable)
	}
	return s
}

func ean13ColumnMetadata(t *testing.T, pg bool, columns []*sql.ColumnType, names, types []string, nullable []bool) {
	t.Helper()
	if len(columns) != len(names) {
		t.Fatalf("column count = %d, want %d", len(columns), len(names))
	}
	for i, col := range columns {
		if col.Name() != names[i] {
			t.Fatalf("column name = %q, want %q", col.Name(), names[i])
		}
		if nullable != nil {
			wantKnown, wantNull := !pg, nullable[i]
			if pg {
				wantNull = false
			}
			if n, ok := col.Nullable(); n != wantNull || ok != wantKnown {
				t.Fatalf("%s nullable = %v/%v, want %v/%v", col.Name(), n, ok, wantNull, wantKnown)
			}
		}
		if types[i] != "EAN13" && types[i] != "ISBN" && types[i] != "ISBN13" && types[i] != "ISMN" && types[i] != "ISMN13" && types[i] != "ISSN" && types[i] != "ISSN13" {
			continue
		}
		wantName, wantScan := types[i], reflect.TypeFor[string]()
		if pg {
			wantName, wantScan = "", reflect.TypeFor[any]()
		}
		length, lengthOK := col.Length()
		precision, scale, decimalOK := col.DecimalSize()
		if col.DatabaseTypeName() != wantName || col.ScanType() != wantScan {
			t.Fatalf("%s type = %q/%v, want %q/%v", types[i], col.DatabaseTypeName(), col.ScanType(), wantName, wantScan)
		}
		if length != 0 || lengthOK {
			t.Fatalf("%s Length = (%d,%v), want (0,false)", types[i], length, lengthOK)
		}
		if precision != 0 || scale != 0 || decimalOK {
			t.Fatalf("%s DecimalSize = (%d,%d,%v), want (0,0,false)", types[i], precision, scale, decimalOK)
		}
	}
}

func ean13Held(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stmt.Close() })
	return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
}
