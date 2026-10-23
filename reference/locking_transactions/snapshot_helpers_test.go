package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func snapshotScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func snapshotType(b fixture.Backend, native string) string {
	if b.PG {
		return ""
	}
	return native
}

func snapshotBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, s fixture.Step) {
	t.Helper()
	if s.SelectRows && s.CheckRaw == nil {
		s = snapshotRawStep(s)
	}
	identityBoth(t, p, b, x, s)
}

// pq leaves pg_snapshot and xid8 unnamed; their raw payload is still bytes.
// Keep descriptor and normalized result assertions in the ordinary fixture path.
func snapshotRawStep(s fixture.Step) fixture.Step {
	check := func(t *testing.T, col, row int, v any) bool {
		t.Helper()
		switch s.Types[col] {
		case "", "PG_SNAPSHOT", "XID8":
		default:
			return false
		}
		if row >= len(s.Rows) {
			t.Fatalf("unexpected snapshot row %d", row)
		}
		want := s.Rows[row][col]
		if want == nil {
			if v != nil {
				t.Fatalf("raw column %d row %d got %T %v want SQL NULL", col, row, v, v)
			}
			return true
		}
		got, ok := v.([]byte)
		if !ok || got == nil || string(got) != want {
			t.Fatalf("raw column %d row %d got %T %v want bytes %v", col, row, v, v, want)
		}
		return true
	}
	s.CheckRaw = check
	previousScan := s.CheckScan
	s.CheckScan = func(t *testing.T, rows *sql.Rows, row int) {
		t.Helper()
		values, destinations := make([]any, len(s.Types)), make([]any, len(s.Types))
		for i := range destinations {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		for col, value := range values {
			check(t, col, row, value)
		}
		if previousScan != nil {
			previousScan(t, rows, row)
		}
	}
	return s
}

func snapshotConn(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c := advisorySessionConn(t, p, b)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), snapshotCleanupTimeout)
		defer cancel()
		_, _ = c.ExecContext(ctx, "ROLLBACK")
	})
	return c
}

func snapshotQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Parse the public serialization independently of any engine type or decoder.
type snapshotText struct {
	min, max uint64
	members  []uint64
}

func snapshotParse(t *testing.T, s string) snapshotText {
	t.Helper()
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		t.Fatalf("snapshot syntax %q", s)
	}
	parse := func(s string) uint64 {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil || strconv.FormatUint(n, 10) != s {
			t.Fatalf("noncanonical snapshot number %q: %v", s, err)
		}
		return n
	}
	v := snapshotText{min: parse(parts[0]), max: parse(parts[1])}
	if v.min == 0 || v.max < v.min {
		t.Fatalf("snapshot bounds %q", s)
	}
	if parts[2] != "" {
		for _, raw := range strings.Split(parts[2], ",") {
			n := parse(raw)
			if n < v.min || n >= v.max || (len(v.members) > 0 && n <= v.members[len(v.members)-1]) {
				t.Fatalf("snapshot membership/order %q", s)
			}
			v.members = append(v.members, n)
		}
	}
	return v
}
func (s snapshotText) contains(n uint64) bool {
	for _, m := range s.members {
		if m == n {
			return true
		}
	}
	return false
}
func snapshotNumber(t *testing.T, s string) uint64 {
	t.Helper()
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func snapshotRead(t *testing.T, p *fixture.Pair, x identityExecutor, held *sql.Stmt) string {
	t.Helper()
	var s string
	var err error
	if held != nil {
		err = held.QueryRowContext(p.Ctx).Scan(&s)
	} else {
		err = x.QueryRowContext(p.Ctx, "SELECT pg_current_snapshot()::text").Scan(&s)
	}
	if err != nil {
		t.Fatal(err)
	}
	snapshotParse(t, s)
	return s
}
func snapshotVisible(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, id, s string, want bool) {
	t.Helper()
	snapshotBoth(t, p, b, x, fixture.Select("SELECT pg_visible_in_snapshot($1::xid8,$2::pg_snapshot) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{want}}, id, s))
}
func snapshotBump(t *testing.T, p *fixture.Pair, c *sql.Conn, older string) string {
	t.Helper()
	fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
	id := identityID(t, p, c)
	if snapshotNumber(t, id) <= snapshotNumber(t, older) {
		t.Fatalf("bumper %s not newer than %s", id, older)
	}
	fixture.LockExec(t, p.Ctx, c, "COMMIT", 0)
	return id
}
func snapshotPeerPresent(t *testing.T, s, id string, present bool) {
	t.Helper()
	v, n := snapshotParse(t, s), snapshotNumber(t, id)
	if present && (n < v.min || n >= v.max) {
		t.Fatalf("peer %s outside bounds of %s", id, s)
	}
	if v.contains(n) != present {
		t.Fatalf("snapshot %s peer %s membership want %v", s, id, present)
	}
}
