package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func rangeLiteral(input string) string {
	return "'" + strings.ReplaceAll(input, "'", "''") + "'"
}

// Check both the public any-scan and middleware-owned stream before normalization.
// A nil nullable slice asserts unknown native nullability for computed outputs;
// a nonnil slice asserts known declaration/aggregate nullability.
func rangeSelect(backend fixture.Backend, query string, columns, types []string, values [][]any, nullable []bool) fixture.Step {
	s := fixture.Select(query, columns, types, values)
	// pq's pinned OID inventory predates multiranges: their actual transport
	// type names are empty, while the native descriptor names remain concrete.
	// Retain logical names separately for raw transport and metadata assertions.
	if backend.PG {
		s.Types = append([]string(nil), types...)
		for i, name := range types {
			if strings.HasSuffix(name, "MULTIRANGE") {
				s.Types[i] = ""
			}
		}
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if !rangeTransportType(types[column]) {
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
			if !rangeTransportType(types[i]) {
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
			if backend.PG || nullable == nil {
				if n || known {
					t.Fatalf("unknown nullable %v/%v, want false/false", n, known)
				}
			} else if !known || n != nullable[i] {
				t.Fatalf("native nullable %v/%v, want %v/true", n, known, nullable[i])
			}
		}
	}
	return s
}

func rangeTransportType(name string) bool {
	switch name {
	case "INT4RANGE", "INT8RANGE", "NUMRANGE", "DATERANGE", "TSRANGE", "TSTZRANGE", "INT4MULTIRANGE", "INT8MULTIRANGE", "NUMMULTIRANGE":
		return true
	}
	return false
}
