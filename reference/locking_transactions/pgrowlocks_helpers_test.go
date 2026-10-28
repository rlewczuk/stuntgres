package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func pgrowScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			// The common fixture resets public, not pg_extension. Drop the
			// provider itself even if a prior reset already removed its members.
			// Register before owner/worker cleanups so no live handle can block
			// teardown, and use a dedicated connection unaffected by SET ROLE.
			cleaner, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if _, err := cleaner.ExecContext(ctx, "DROP EXTENSION IF EXISTS pgrowlocks CASCADE"); err != nil {
					t.Error("pgrowlocks extension cleanup", err)
				}
				if err := cleaner.Close(); err != nil {
					t.Error(err)
				}
			})
			fixture.LockExec(t, p.Ctx, cleaner, "DROP EXTENSION IF EXISTS pgrowlocks CASCADE", 0)
			a, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			body(t, p, b, a, observer)
		})
	}
}

var pgrowColumns = []string{"locked_row", "locker", "multi", "xids", "modes", "pids"}
var pgrowTypes = []string{"TID", "XID", "BOOL", "_XID", "_TEXT", "_INT4"}

type pgrowMember struct {
	xid, mode string
	pid       int64
}
type pgrowRecord struct {
	tid, locker string
	multi       bool
	members     []pgrowMember
}

func pgrowInstall(t *testing.T, p *fixture.Pair, a fixture.Executor) {
	t.Helper()
	fixture.LockExec(t, p.Ctx, a, "CREATE EXTENSION pgrowlocks", 0)
}
func pgrowTable(t *testing.T, p *fixture.Pair, a fixture.Executor) {
	t.Helper()
	pgrowInstall(t, p, a)
	fixture.LockExec(t, p.Ctx, a, "CREATE TABLE pgrow_t(id INT PRIMARY KEY,v INT)", 0)
	fixture.LockExec(t, p.Ctx, a, "INSERT INTO pgrow_t VALUES(1,0)", 1)
}
func pgrowRead(t *testing.T, rows *sql.Rows) []pgrowRecord {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, pgrowColumns) {
		t.Fatalf("pgrowlocks columns %v (%v), want %v", columns, err, pgrowColumns)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 6 {
		t.Fatalf("pgrowlocks descriptors %v (%v)", types, err)
	}
	for i, typ := range types {
		if typ.DatabaseTypeName() != pgrowTypes[i] {
			t.Fatalf("column %s type %q want %q", columns[i], typ.DatabaseTypeName(), pgrowTypes[i])
		}
	}
	var result []pgrowRecord
	for rows.Next() {
		var r pgrowRecord
		var xids, modes []string
		var pids []int64
		if err := rows.Scan(&r.tid, &r.locker, &r.multi, pq.Array(&xids), pq.Array(&modes), pq.Array(&pids)); err != nil {
			t.Fatal(err)
		}
		var block uint64
		var offset uint64
		if n, err := fmt.Sscanf(r.tid, "(%d,%d)", &block, &offset); n != 2 || err != nil || block >= 4294967295 || offset == 0 || offset > 65535 {
			t.Fatalf("invalid nonzero tuple identity %q: %v", r.tid, err)
		}
		if n, err := strconv.ParseUint(r.locker, 10, 32); err != nil || n == 0 {
			t.Fatalf("invalid raw locker %q: %v", r.locker, err)
		}
		if len(xids) == 0 || len(xids) != len(modes) || len(xids) != len(pids) {
			t.Fatalf("unaligned member arrays: %v %v %v", xids, modes, pids)
		}
		for i, xid := range xids {
			if n, err := strconv.ParseUint(xid, 10, 32); err != nil || n < 3 || pids[i] < 0 || pids[i] > 2147483647 {
				t.Fatalf("invalid member identity %q pid %d: %v", xid, pids[i], err)
			}
			r.members = append(r.members, pgrowMember{xid, modes[i], pids[i]})
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
func pgrowQuery(t *testing.T, p *fixture.Pair, x fixture.Executor, q string, args ...any) []pgrowRecord {
	t.Helper()
	rows, err := x.QueryContext(p.Ctx, q, args...)
	if err != nil {
		t.Fatal(q, err)
	}
	return pgrowRead(t, rows)
}
func pgrowGet(t *testing.T, p *fixture.Pair, x fixture.Executor) []pgrowRecord {
	t.Helper()
	return pgrowQuery(t, p, x, "SELECT * FROM pgrowlocks($1::text)", "pgrow_t")
}
func pgrowEmpty(t *testing.T, p *fixture.Pair, x fixture.Executor) {
	t.Helper()
	if got := pgrowGet(t, p, x); len(got) != 0 {
		t.Fatalf("expected no locked visible tuples, got %+v", got)
	}
}
func pgrowOne(t *testing.T, got []pgrowRecord, multi bool, want ...pgrowMember) pgrowRecord {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("locked tuple bag %+v, want exactly one", got)
	}
	r := got[0]
	order := func(v []pgrowMember) {
		sort.Slice(v, func(i, j int) bool {
			if v[i].xid != v[j].xid {
				return v[i].xid < v[j].xid
			}
			if v[i].mode != v[j].mode {
				return v[i].mode < v[j].mode
			}
			return v[i].pid < v[j].pid
		})
	}
	actual, expected := append([]pgrowMember(nil), r.members...), append([]pgrowMember(nil), want...)
	order(actual)
	order(expected)
	if r.multi != multi || !reflect.DeepEqual(actual, expected) {
		t.Fatalf("raw tuple %+v, want multi=%v aligned member bag %+v", r, multi, expected)
	}
	if !multi && (len(want) != 1 || r.locker != want[0].xid) {
		t.Fatalf("singleton locker %q must equal sole actual xid: %+v", r.locker, want)
	}
	return r
}
func pgrowOwner(t *testing.T, p *fixture.Pair, a *sql.Conn, mode string) pgrowMember {
	t.Helper()
	xid := identityLow(t, identityID(t, p, a))
	return pgrowMember{xid, mode, int64(pgrowTransactionPID(t, p, a, xid))}
}

// Read identity from the independently assigned transaction's real lock, not
// from either the pgrowlocks result under test or the SQL backend inquiry.
func pgrowTransactionPID(t *testing.T, p *fixture.Pair, conn *sql.Conn, xid string) int {
	t.Helper()
	rows, err := conn.QueryContext(p.Ctx, "SELECT pid FROM pg_locks WHERE locktype='transactionid' AND transactionid=$1::xid AND mode='ExclusiveLock' AND granted", xid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var pid int
	if !rows.Next() {
		t.Fatal("assigned transaction has no granted identity lock", rows.Err())
	}
	if err := rows.Scan(&pid); err != nil || pid <= 0 {
		t.Fatal("assigned transaction PID", pid, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("assigned transaction has multiple identity locks", rows.Err())
	}
	return pid
}

// A temporary transaction identifies an otherwise idle waiter. Roll it back
// before queuing so cancellation/replacement keep their autocommit semantics.
func pgrowWaiterPID(t *testing.T, p *fixture.Pair, conn *sql.Conn) int {
	t.Helper()
	fixture.LockExec(t, p.Ctx, conn, "BEGIN", 0)
	defer fixture.LockExec(t, p.Ctx, conn, "ROLLBACK", 0)
	xid := identityLow(t, identityID(t, p, conn))
	return pgrowTransactionPID(t, p, conn, xid)
}
func pgrowSameTID(t *testing.T, a, b pgrowRecord) {
	t.Helper()
	if a.tid != b.tid {
		t.Fatalf("unchanged version changed identity %s -> %s", a.tid, b.tid)
	}
}

// Assert the full public diagnostic, including independently expected empty
// fields. Errorf allows oracle discovery to report all diagnostic rows in a case.
func pgrowError(t *testing.T, p *fixture.Pair, x fixture.Executor, q, stage string, want fixture.Diagnostic, args ...any) {
	t.Helper()
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		var err error
		if prepared {
			stmt, e := x.PrepareContext(p.Ctx, q)
			err = e
			if stage == "execute" && err != nil {
				t.Errorf("runtime-only failure occurred at Prepare for %s: %v", q, err)
			}
			if stage == "prepare" && stmt != nil {
				stmt.Close()
				t.Errorf("invalid Prepare returned handle for %s", q)
			}
			if stage != "prepare" && err == nil {
				rows, err = stmt.QueryContext(p.Ctx, args...)
			}
			if stmt != nil {
				stmt.Close()
			}
		} else {
			rows, err = x.QueryContext(p.Ctx, q, args...)
		}
		if rows != nil {
			rows.Close()
			t.Errorf("failed query returned Rows: %s", q)
		}
		if err == nil {
			t.Errorf("expected %s for prepared=%v: %s", want.Code, prepared, q)
			continue
		}
		got := fixture.ExtractDiagnostic(t, err)
		if got != want {
			t.Errorf("%s prepared=%v: got %#v want %#v", q, prepared, got, want)
		}
	}
}
func pgrowPosition(q, token string) string {
	return strconv.Itoa(len([]rune(q[:strings.Index(q, token)])) + 1)
}

// Public SQL establishes the actual waiter/request, not elapsed time or worker
// start. The same pg_locks and pg_blocking_pids relationships apply in both modes.
func pgrowWait(t *testing.T, p *fixture.Pair, observer *sql.Conn, waiter, holder int, relation bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	q := "SELECT $2=ANY(pg_blocking_pids($1))"
	if relation {
		q += " AND EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='relation' AND relation='pgrow_t'::regclass AND mode='AccessShareLock' AND NOT granted)"
	}
	for ctx.Err() == nil {
		var ready bool
		if err := observer.QueryRowContext(ctx, q, waiter, holder).Scan(&ready); err != nil {
			t.Fatal(err)
		}
		if ready {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("no actual pgrowlocks blocking request", ctx.Err())
}
