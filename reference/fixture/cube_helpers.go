package fixture

import (
	"database/sql"
	"fmt"
	"testing"
)

// Expectations were independently specified and confirmed against the pinned
// PostgreSQL 18.6 oracle; native failures must not redefine them.
func CubeSelect(b Backend, q string, columns []string, values [][]any, args ...any) Step {
	types := make([]string, len(columns))
	for i, name := range columns {
		if name == "id" {
			types[i] = "INT4"
		} else if !b.PG {
			types[i] = "CUBE"
		}
	}
	s := Select(q, columns, types, values, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if columns[column] == "id" {
			return false
		}
		if value == nil {
			if values[row][column] != nil {
				t.Fatalf("cube cell %d/%d unexpectedly NULL", row, column)
			}
			return true
		}
		if values[row][column] == nil {
			t.Fatalf("cube cell %d/%d = %T, want NULL", row, column, value)
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
	return s
}

func CubeComparisonTemporaryIDs(t *testing.T, p *Pair, c *sql.Conn) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, part := range []struct{ table, name, filter string }{
		{"pg_class", "relname", "relnamespace=pg_my_temp_schema()"},
		{"pg_type", "typname", "typnamespace=pg_my_temp_schema()"},
		{"pg_namespace", "nspname", "(nspname LIKE 'pg_temp_%' OR nspname LIKE 'pg_toast_temp_%') AND NOT pg_is_other_temp_schema(oid)"},
	} {
		rows, err := c.QueryContext(p.Ctx, "SELECT "+part.name+",oid::bigint FROM "+part.table+" WHERE "+part.filter+" ORDER BY oid")
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var name string
			var id int64
			if err := rows.Scan(&name, &id); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if id <= 0 {
				rows.Close()
				t.Fatal("invalid retained identity", id)
			}
			out[part.table+"/"+name] = id
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
	}
	return out
}

func CubeComparisonForceCollisions(t *testing.T, p *Pair, c *sql.Conn, source map[string]int64) map[string]int64 {
	t.Helper()
	var maximum int64
	for _, id := range source {
		maximum = max(maximum, id)
	}
	create := func(i int) {
		RunStep(t, p.Ctx, c, p.MemoryRecord, Exec(fmt.Sprintf("CREATE TEMP TABLE cube_compare_retained_%d(v integer)", i), 0))
	}
	create(0)
	retained := CubeComparisonTemporaryIDs(t, p, c)
	var start, last int64
	for _, id := range retained {
		if start == 0 || id < start {
			start = id
		}
		last = max(last, id)
	}
	if start == 0 || start > maximum {
		t.Fatalf("retained allocation starts beyond source graph: %d/%d", start, maximum)
	}
	// At least one newly observed identity per table is required below; this
	// bound follows from the observed interval, not a guessed objects-per-DDL.
	bound := maximum - start + 1
	for i := int64(1); last < maximum && i <= bound; i++ {
		create(int(i))
		retained = CubeComparisonTemporaryIDs(t, p, c)
		var next int64
		for _, id := range retained {
			next = max(next, id)
		}
		if next <= last {
			t.Fatal("retained allocator made no progress", last, next)
		}
		last = next
	}
	occupied := map[int64]bool{}
	for _, id := range retained {
		occupied[id] = true
	}
	for name, id := range source {
		if !occupied[id] {
			t.Fatalf("retained identity set has a hole at %s=%d: %v", name, id, retained)
		}
	}
	RunStep(t, p.Ctx, c, p.MemoryRecord, Exec("INSERT INTO pg_temp.cube_compare_retained_0 VALUES (73)", 1))
	return retained
}
