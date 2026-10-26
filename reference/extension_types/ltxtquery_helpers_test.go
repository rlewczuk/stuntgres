package reference_test

import (
 "database/sql"
 "reflect"
 "testing"
 "stuntgres/reference/fixture"
)

func ltxtquerySelect(b fixture.Backend, q string, columns []string, values [][]any, args ...any) fixture.Step {
	types := make([]string, len(columns))
	for i, name := range columns {
		if name == "id" { types[i] = "INT4" } else if !b.PG { types[i] = "LTXTQUERY" }
	}
	s := fixture.Select(q, columns, types, values, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if columns[column] == "id" { return false }
		if value == nil { return true }
		if b.PG {
			if _, ok := value.([]byte); !ok { t.Fatalf("pq ltxtquery raw value %T", value) }
		} else if _, ok := value.(string); !ok { t.Fatalf("native ltxtquery raw value %T", value) }
		return true
	}
	s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
		raw := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range raw { dest[i] = &raw[i] }
		if err := rows.Scan(dest...); err != nil { t.Fatal(err) }
		for i, value := range raw {
			if columns[i] == "id" || value == nil { continue }
			if b.PG {
				if _, ok := value.([]byte); !ok { t.Fatalf("pq ltxtquery raw value %T", value) }
			} else if _, ok := value.(string); !ok { t.Fatalf("native ltxtquery raw value %T", value) }
		}
	}
	return s
}

func ltxtqueryMetadata(t *testing.T, pg bool, rows *sql.Rows, names []string, nullable []bool) []*sql.ColumnType {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil { t.Fatal(err) }
	ltxtqueryColumnMetadata(t, pg, columns, names, nullable)
	return columns
}

func ltxtqueryColumnMetadata(t *testing.T, pg bool, columns []*sql.ColumnType, names []string, nullable []bool) {
	t.Helper()
	if len(columns) != len(names) { t.Fatalf("columns %d want %d", len(columns), len(names)) }
	for i, c := range columns {
		if c.Name() != names[i] { t.Fatalf("column name %q want %q", c.Name(), names[i]) }
		if names[i] == "id" { continue }
		name, scan := "LTXTQUERY", reflect.TypeFor[string]()
		if pg { name, scan = "", reflect.TypeFor[any]() }
		if c.DatabaseTypeName() != name || c.ScanType() != scan { t.Fatalf("type %q/%v want %q/%v", c.DatabaseTypeName(), c.ScanType(), name, scan) }
		if n, known := c.Nullable(); pg || nullable == nil {
			if n || known { t.Fatalf("computed/pq nullability %v/%v", n, known) }
		} else if !known || n != nullable[i] { t.Fatalf("nullable %v/%v want %v/true", n, known, nullable[i]) }
		if n, known := c.Length(); n != 0 || known { t.Fatalf("length %d/%v", n, known) }
		if p, s, known := c.DecimalSize(); p != 0 || s != 0 || known { t.Fatalf("decimal %d/%d/%v", p, s, known) }
	}
}
