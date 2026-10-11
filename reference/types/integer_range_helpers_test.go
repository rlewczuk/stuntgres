package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func integerRangeLiteral(input string) string {
	return "'" + strings.ReplaceAll(input, "'", "''") + "'"
}

// Check both the public any-scan and middleware-owned stream before normalization.
func integerRangeSelect(backend fixture.Backend, query string, columns, types []string, values [][]any, nullable []bool) fixture.Step {
	s := fixture.Select(query, columns, types, values)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if types[column] != "INT4RANGE" && types[column] != "INT8RANGE" {
			return false
		}
		if value != nil {
			want := reflect.TypeFor[string]()
			if backend.PG {
				want = reflect.TypeFor[[]byte]()
			}
			if reflect.TypeOf(value) != want {
				t.Fatalf("range column %d row %d raw %T, want %v", column, row, value, want)
			}
			if bytes, ok := value.([]byte); ok && bytes == nil {
				t.Fatal("typed nil bytes are not SQL NULL")
			}
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		metadata, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, typ := range metadata {
			if types[i] != "INT4RANGE" && types[i] != "INT8RANGE" {
				continue
			}
			want := reflect.TypeFor[string]()
			if backend.PG {
				want = reflect.TypeFor[any]()
			}
			if typ.ScanType() != want {
				t.Fatalf("%s ScanType %v, want %v", types[i], typ.ScanType(), want)
			}
			if length, known := typ.Length(); length != 0 || known {
				t.Fatalf("%s length %d/%v", types[i], length, known)
			}
			if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
				t.Fatalf("%s precision/scale %d/%d/%v", types[i], precision, scale, known)
			}
			n, known := typ.Nullable()
			if backend.PG {
				if n || known {
					t.Fatalf("pq nullable %v/%v", n, known)
				}
			} else if !known || n != nullable[i] {
				t.Fatalf("native nullable %v/%v, want %v/true", n, known, nullable[i])
			}
		}
	}
	return s
}
