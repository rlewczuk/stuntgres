package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Independent REL_18_6 declarations; the OUT spelling is an oracle-first
// expectation from pg_proc.dat, not the conflicting manual/C tuple label.
var stateFunctions = []string{"pg_stat_get_backend_activity", "pg_stat_get_backend_activity_start", "pg_stat_get_backend_xact_start", "pg_stat_get_backend_wait_event", "pg_stat_get_backend_wait_event_type", "pg_stat_get_backend_subxact"}
var stateTypes = []string{"TEXT", "TIMESTAMPTZ", "TIMESTAMPTZ", "TEXT", "TEXT", "RECORD"}
var stateNames = []string{"activity", "activity_start", "xact_start", "wait_event", "wait_event_type", "subxact"}
var stateSubNames = []string{"subxact_count", "subxact_overflowed"}
var stateSubTypes = []string{"INT4", "BOOL"}

const stateSQL = "SELECT pg_stat_get_backend_activity($1) AS activity,pg_stat_get_backend_activity_start($1) AS activity_start,pg_stat_get_backend_xact_start($1) AS xact_start,pg_stat_get_backend_wait_event($1) AS wait_event,pg_stat_get_backend_wait_event_type($1) AS wait_event_type,pg_stat_get_backend_subxact($1) AS subxact"
const stateMissing = "<backend information not available>"
const stateDenied = "<insufficient privilege>"

func stateRows(t *testing.T, rows *sql.Rows, pg bool, names, types []string) [][]any {
	t.Helper()
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil || len(cols) != len(types) {
		t.Fatal("state descriptors", cols, err)
	}
	for i, c := range cols {
		if c.Name() != names[i] || c.DatabaseTypeName() != types[i] {
			t.Fatal("state descriptor", i, c.Name(), c.DatabaseTypeName(), names, types)
		}
		if _, known := c.Nullable(); known {
			t.Fatal("computed state nullability must be unknown", i)
		}
		want := reflect.TypeOf("")
		switch types[i] {
		case "TIMESTAMPTZ":
			want = reflect.TypeOf(time.Time{})
		case "INT4":
			want = reflect.TypeOf(int64(0))
			if pg {
				want = reflect.TypeOf(int32(0))
			}
		case "BOOL":
			want = reflect.TypeOf(false)
		case "RECORD":
			if pg {
				want = reflect.TypeFor[any]()
			}
		}
		if c.ScanType() != want {
			t.Fatalf("%s ScanType %v want %v", names[i], c.ScanType(), want)
		}
	}
	result := [][]any{}
	for rows.Next() {
		row := make([]any, len(types))
		dst := make([]any, len(types))
		for i := range row {
			dst[i] = &row[i]
		}
		if err := rows.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		for i, v := range row {
			if v == nil {
				continue
			}
			want := reflect.TypeOf("")
			switch types[i] {
			case "TIMESTAMPTZ":
				want = reflect.TypeOf(time.Time{})
			case "INT4":
				want = reflect.TypeOf(int64(0))
			case "BOOL":
				want = reflect.TypeOf(false)
			case "RECORD":
				if pg {
					want = reflect.TypeOf([]byte{})
				}
			}
			if reflect.TypeOf(v) != want {
				t.Fatalf("%s raw carrier %T want %v", names[i], v, want)
			}
			if data, ok := v.([]byte); ok {
				row[i] = string(data)
			}
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func stateRead(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, id any) []any {
	t.Helper()
	r, err := e.QueryContext(p.Ctx, stateSQL, id)
	if err != nil {
		t.Fatal(err)
	}
	got := stateRows(t, r, b.PG, stateNames, stateTypes)
	if len(got) != 1 {
		t.Fatal("six scalar cardinality", got)
	}
	return got[0]
}

func stateEqual(t *testing.T, got, want []any) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatal(got, want)
	}
	for i := range want {
		if !reflect.DeepEqual(fixture.Normalize(got[i]), fixture.Normalize(want[i])) {
			t.Fatalf("state field %d got %T %v want %T %v", i, got[i], got[i], want[i], want[i])
		}
	}
}

func stateBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
}

func stateSub(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn, id any, count int64, overflow, fresh bool) {
	t.Helper()
	if fresh {
		statActivityClear(t, p, o)
	}
	step := fixture.Select("SELECT * FROM pg_stat_get_backend_subxact($1)", stateSubNames, stateSubTypes, [][]any{{count, overflow}}, id)
	stateBoth(t, p, b, o, step)
}

func stateIdle(t *testing.T, pg bool, row []any) {
	t.Helper()
	want := []any{nil, nil}
	if pg {
		want = []any{"ClientRead", "Client"}
	}
	stateEqual(t, row[3:5], want)
}

func stateMapped(t *testing.T, p *fixture.Pair, b fixture.Backend, o, peer *sql.Conn) []any {
	t.Helper()
	return identityMapped(t, p, b, o, peer)
}

func stateAbsent(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn, id int64) {
	t.Helper()
	for _, row := range identitySet(t, p, b, o) {
		if row[0] == id {
			t.Fatal("missing-ID candidate exists", id)
		}
	}
	stateEqual(t, stateRead(t, p, b, o, id), []any{stateMissing, nil, nil, stateMissing, stateMissing, "(,)"})
}

func stateSource(t *testing.T, row []any, activity statActivityRow, query string) {
	t.Helper()
	stateEqual(t, row[:3], []any{query, activity[12], activity[11]})
	stamp, ok := row[1].(time.Time)
	if !ok || stamp.IsZero() {
		t.Fatal("genuine query time", row)
	}
}

func stateMask(t *testing.T, got, visible []any, allowed bool) {
	t.Helper()
	if allowed {
		stateEqual(t, got, visible)
		return
	}
	stateEqual(t, got, []any{stateDenied, nil, nil, stateDenied, stateDenied, visible[5]})
}

func statePrepare(t *testing.T, p *fixture.Pair, c *sql.Conn) fixture.HeldStatement {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, stateSQL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return fixture.HeldStatement{Executor: c, Statement: s, SQL: stateSQL}
}
