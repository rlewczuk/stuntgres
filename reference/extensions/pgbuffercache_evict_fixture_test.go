package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var bceSingleNames = []string{"buffer_evicted", "buffer_flushed"}
var bceSingleTypes = []string{"BOOL", "BOOL"}
var bceCountNames = []string{"buffers_evicted", "buffers_flushed", "buffers_skipped"}
var bceCountTypes = []string{"INT4", "INT4", "INT4"}

func bceHeap(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	bcDDL(t, p, b, c, "CREATE TABLE bc_part."+name+"(k integer)")
	bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part."+name+" VALUES(1)", 1))
}

func bceWarm(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) [][]any {
	t.Helper()
	bcReadHeap(t, p, b, c, "SELECT k FROM bc_part."+name)
	rows := bcTarget(t, p, b, c, "bc_part."+name)
	if len(rows) == 0 || (!b.PG && len(rows) != 1) {
		t.Fatal("actual read did not occupy target storage", name, rows)
	}
	return rows
}

func bceClean(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	if b.PG {
		// A real checkpoint, not a fabricated native dirty/writeback facility.
		bcDDL(t, p, b, c, "CHECKPOINT")
	}
}

func bceCall(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, prepared, single bool, query string, args ...any) []any {
	t.Helper()
	var st *sql.Stmt
	var err error
	if prepared {
		st, err = c.PrepareContext(p.Ctx, query)
		fixture.State(t, err, "")
		defer st.Close()
	}
	var rows *sql.Rows
	if st != nil {
		rows, err = st.QueryContext(p.Ctx, args...)
	} else {
		rows, err = c.QueryContext(p.Ctx, query, args...)
	}
	fixture.State(t, err, "")
	names, types := bceCountNames, bceCountTypes
	if single {
		names, types = bceSingleNames, bceSingleTypes
	}
	result := bcRows(t, rows, b.PG, false, names, types)
	if len(result) != 1 {
		t.Fatal("non-SETOF eviction must produce exactly one row", query, result)
	}
	return result[0]
}

func bceExact(t *testing.T, got []any, want ...any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatal("performed eviction result", got, "want", want)
	}
}

func bceCounts(t *testing.T, b fixture.Backend, got []any, capacity, evicted, skipped int64) {
	t.Helper()
	if !b.PG {
		bceExact(t, got, evicted, int64(0), skipped)
		return
	}
	// Released bufmgr counts occupied candidate attempts, not unused slots.
	// PG has real physical forks and concurrent shared-cache users. Native's
	// exact representative-block totals are not PostgreSQL counter goldens.
	e, f, s := got[0].(int64), got[1].(int64), got[2].(int64)
	if e < evicted || f < 0 || s < 0 || e+s > capacity || f > e+s {
		t.Fatal("physical eviction counter domain", got, "capacity", capacity, "minimum evicted", evicted)
	}
}

func bceCold(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	if got := bcTarget(t, p, b, c, "bc_part."+name); len(got) != 0 {
		t.Fatal("evicted target still occupies cache", name, got)
	}
}

func bceOccupied(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) int64 {
	t.Helper()
	var n int64
	if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM bc_part.pg_buffercache WHERE relfilenode IS NOT NULL").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func bceNullStep(query string, single bool, args ...any) fixture.Step {
	names, types := bceCountNames, bceCountTypes
	if single {
		names, types = bceSingleNames, bceSingleTypes
	}
	return fixture.Select(query, names, types, [][]any{make([]any, len(names))}, args...)
}

func bceHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) fixture.HeldStatement {
	t.Helper()
	st, err := c.PrepareContext(p.Ctx, query)
	fixture.State(t, err, "")
	t.Cleanup(func() { st.Close() })
	return fixture.HeldStatement{Executor: c, Statement: st, SQL: query}
}

func bceRuntimeDenial(query, name string) fixture.Step {
	s := bcError(query, "42501", fmt.Sprintf("must be superuser to use %s()", name))
	s.ErrorStage = "execute"
	return s
}

func bceScalarMetadata(s fixture.Step, pg bool) fixture.Step {
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		cols, err := rows.ColumnTypes()
		if err != nil || len(cols) != 1 {
			t.Fatal("scalar record descriptor", cols, err)
		}
		col := cols[0]
		if col.Name() != "result" || col.DatabaseTypeName() != "RECORD" {
			t.Fatal("scalar record name/type", col)
		}
		want := reflect.TypeFor[string]()
		if pg {
			want = reflect.TypeFor[any]()
		}
		if col.ScanType() != want {
			t.Fatal("scalar record ScanType", col.ScanType(), want)
		}
		if nullable, known := col.Nullable(); nullable || known {
			t.Fatal("computed scalar nullable metadata", nullable, known)
		}
		if n, known := col.Length(); n != 0 || known {
			t.Fatal("scalar record length", n, known)
		}
		if precision, scale, known := col.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("scalar record decimal metadata", precision, scale, known)
		}
	}
	return s
}

func bceMainSlot(t *testing.T, rows [][]any) any {
	t.Helper()
	for _, row := range rows {
		if row[4] == int64(0) && row[5] == int64(0) {
			return row[0]
		}
	}
	t.Fatal("actual main-fork block zero absent", rows)
	return nil
}

func bceScalarCall(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, single bool, query string, args ...any) []any {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, query, args...)
	fixture.State(t, err, "")
	defer rows.Close()
	bceScalarMetadata(fixture.Step{}, b.PG).CheckMetadata(t, rows)
	if !rows.Next() {
		t.Fatal("missing scalar composite", rows.Err())
	}
	var value any
	if err := rows.Scan(&value); err != nil {
		t.Fatal(err)
	}
	text, ok := fixture.Normalize(value).(string)
	if !ok {
		t.Fatalf("scalar composite transport %T(%v)", value, value)
	}
	var result []any
	if single {
		switch text {
		case "(t,f)":
			result = []any{true, false}
		case "(f,f)":
			result = []any{false, false}
		case "(t,t)":
			result = []any{true, true}
		case "(f,t)":
			result = []any{false, true}
		default:
			t.Fatal("invalid boolean composite encoding", text)
		}
	} else {
		var evicted, flushed, skipped int64
		if n, err := fmt.Sscanf(text, "(%d,%d,%d)", &evicted, &flushed, &skipped); err != nil || n != 3 || text != fmt.Sprintf("(%d,%d,%d)", evicted, flushed, skipped) {
			t.Fatal("invalid integer composite encoding", text, n, err)
		}
		result = []any{evicted, flushed, skipped}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("scalar composite cardinality", rows.Err())
	}
	return result
}
