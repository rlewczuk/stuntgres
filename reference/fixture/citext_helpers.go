package fixture

import "testing"

func CitextSelect(b Backend, q string, names []string, values [][]any, args ...any) Step {
	types := make([]string, len(names))
	for i, n := range names {
		if n == "id" {
			types[i] = "INT4"
		} else if !b.PG {
			types[i] = "CITEXT"
		}
	}
	s := Select(q, names, types, values, args...)
	s.CheckRaw = func(t *testing.T, col, row int, value any) bool {
		if names[col] == "id" {
			return false
		}
		if value == nil {
			return true
		}
		if b.PG {
			if _, ok := value.([]byte); !ok {
				t.Fatalf("citext PG raw cell %d/%d: %T, want []byte", row, col, value)
			}
		} else if _, ok := value.(string); !ok {
			t.Fatalf("citext native raw cell %d/%d: %T, want string", row, col, value)
		}
		return true
	}
	return s
}
