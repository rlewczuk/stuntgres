package fixture

import "testing"

// Values are independently specified. lib/pq's unknown extension OID uses raw
// bytes and an empty type name; native uses its actual QUERY_INT descriptor.
func QueryIntSelect(b Backend, q string, columns []string, values [][]any, args ...any) Step {
	types := make([]string, len(columns))
	for i, name := range columns {
		if name == "id" {
			types[i] = "INT4"
		} else if !b.PG {
			types[i] = "QUERY_INT"
		}
	}
	s := Select(q, columns, types, values, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if columns[column] == "id" {
			return false
		}
		if value == nil {
			return true
		}
		if b.PG {
			if _, ok := value.([]byte); !ok {
				t.Fatalf("query_int raw PG cell %d/%d = %T, want []byte", row, column, value)
			}
		} else if _, ok := value.(string); !ok {
			t.Fatalf("query_int raw native cell %d/%d = %T, want string", row, column, value)
		}
		return true
	}
	return s
}
