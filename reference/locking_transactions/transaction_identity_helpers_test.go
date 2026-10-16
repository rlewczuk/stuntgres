package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

type identityExecutor interface {
	fixture.Executor
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func identityScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			body(t, p, b, a, observer)
		})
	}
}

func identityID(t *testing.T, p *fixture.Pair, x identityExecutor) string {
	t.Helper()
	var id string
	if err := x.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id()::text").Scan(&id); err != nil {
		t.Fatal(err)
	}
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil || uint32(n) < 3 {
		t.Fatalf("invalid assigned identity %q: %v", id, err)
	}
	return id
}
func identityLow(t *testing.T, id string) string {
	t.Helper()
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return strconv.FormatUint(uint64(uint32(n)), 10)
}
func identityUnassigned(t *testing.T, p *fixture.Pair, x identityExecutor) {
	t.Helper()
	var id sql.NullString
	if err := x.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id_if_assigned()::text").Scan(&id); err != nil || id.Valid {
		t.Fatalf("expected unassigned, got %v (%v)", id, err)
	}
}
func identityStatus(t *testing.T, p *fixture.Pair, x identityExecutor, id, want string) {
	t.Helper()
	stmt, err := x.PrepareContext(p.Ctx, "SELECT pg_xact_status($1::xid8)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, prepared := range []bool{false, true} {
		var got string
		var err error
		if prepared {
			err = stmt.QueryRowContext(p.Ctx, id).Scan(&got)
		} else {
			err = x.QueryRowContext(p.Ctx, "SELECT pg_xact_status($1::xid8)", id).Scan(&got)
		}
		if err != nil || got != want {
			t.Fatalf("status(%s) prepared=%v: %q (%v), want %q", id, prepared, got, err, want)
		}
	}
}
func identityBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		step := s
		step.Prepared = prepared
		fixture.RunStep(t, p.Ctx, x, b.Recorder, step)
	}
}
func identityNextMulti(t *testing.T, p *fixture.Pair, x identityExecutor) uint32 {
	t.Helper()
	var age int32
	if err := x.QueryRowContext(p.Ctx, "SELECT mxid_age('1'::xid)").Scan(&age); err != nil {
		t.Fatal(err)
	}
	next := uint32(age) + 1
	if next == 0 {
		t.Fatal("mxid_age exposed reserved next MultiXact 0")
	}
	return next
}

type identityMember struct{ xid, mode string }

func identityBag(t *testing.T, p *fixture.Pair, x fixture.Executor, id uint32, want []identityMember) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		q := "SELECT xid::text,mode FROM pg_get_multixact_members($1::xid)"
		var rows *sql.Rows
		var err error
		if prepared {
			stmt, e := x.PrepareContext(p.Ctx, q)
			if e != nil {
				t.Fatal(e)
			}
			rows, err = stmt.QueryContext(p.Ctx, strconv.FormatUint(uint64(id), 10))
			defer stmt.Close()
		} else {
			rows, err = x.QueryContext(p.Ctx, q, strconv.FormatUint(uint64(id), 10))
		}
		if err != nil {
			t.Fatal(err)
		}
		var got []identityMember
		for rows.Next() {
			var m identityMember
			if err := rows.Scan(&m.xid, &m.mode); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			got = append(got, m)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		order := func(v []identityMember) {
			sort.Slice(v, func(i, j int) bool {
				if v[i].xid != v[j].xid {
					return v[i].xid < v[j].xid
				}
				return v[i].mode < v[j].mode
			})
		}
		expected := append([]identityMember(nil), want...)
		order(expected)
		order(got)
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("MultiXact %d prepared=%v: bag %v want %v", id, prepared, got, expected)
		}
	}
}

// Tiny committed rows have one physical event and no TOAST/page-pressure branch.
// Account for the complete allocated interval, not merely a matching subset.
func identityNewMulti(t *testing.T, p *fixture.Pair, observer identityExecutor, event func(), want []identityMember) uint32 {
	t.Helper()
	before := identityNextMulti(t, p, observer)
	event()
	after := identityNextMulti(t, p, observer)
	next := before + 1
	if next == 0 {
		next = 1
	}
	if after != next {
		t.Fatalf("physical event allocated interval [%d,%d), want exactly one new MultiXact", before, after)
	}
	identityBag(t, p, observer, before, want)
	return before
}
func identityNoMulti(t *testing.T, p *fixture.Pair, observer identityExecutor, event func()) {
	t.Helper()
	before := identityNextMulti(t, p, observer)
	event()
	after := identityNextMulti(t, p, observer)
	if before != after {
		t.Fatalf("non-event advanced MultiXact counter %d -> %d", before, after)
	}
}
func identityLock(t *testing.T, p *fixture.Pair, x fixture.Executor, table string, row int, mode string) {
	t.Helper()
	rows, err := x.QueryContext(p.Ctx, fmt.Sprintf("SELECT id FROM %s WHERE id=%d FOR %s", table, row, mode))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("missing locked row %d: %v", row, rows.Err())
	}
	var got int
	if err := rows.Scan(&got); err != nil || got != row {
		t.Fatalf("row %d: %d %v", row, got, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("unexpected lock result: %v", rows.Err())
	}
}
