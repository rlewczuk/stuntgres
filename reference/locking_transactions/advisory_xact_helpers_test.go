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
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func advisoryXactScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func advisoryXactConn(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c := advisorySessionConn(t, p, b)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := c.ExecContext(ctx, "ROLLBACK"); err != nil && err != sql.ErrConnDone {
			t.Error(err)
		}
	})
	return c
}

// Initialize native identity outside every transaction that might be prepared.
// As in the existing blocking-PID fixtures, the TEMP namespace suffix is the
// public native physical-session identity, independently of the SQL backend inquiry.
func advisoryXactPID(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) int {
	t.Helper()
	if b.PG {
		return fixture.LockPID(t, p.Ctx, c)
	}
	advisorySessionExec(t, p.Ctx, c, "CREATE TEMP SEQUENCE advisory_xact_pid_identity", 0)
	var oid int64
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_my_temp_schema()").Scan(&oid); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := c.QueryRowContext(p.Ctx, "SELECT nspname FROM pg_namespace WHERE oid=$1", oid).Scan(&name); err != nil {
		t.Fatal(err)
	}
	suffix, ok := strings.CutPrefix(name, "pg_temp_")
	id, err := strconv.ParseUint(suffix, 10, 64)
	if !ok || err != nil || id == 0 || id > 2147483647 {
		t.Fatal("native physical identity", name, err)
	}
	return int(id)
}

func advisoryXactName(xact, shared, try bool) string {
	name := "pg_"
	if try {
		name += "try_"
	}
	name += "advisory_"
	if xact {
		name += "xact_"
	}
	name += "lock"
	if shared {
		name += "_shared"
	}
	return name
}

func advisoryXactHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.Executor {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	t.Cleanup(func() {
		if err := stmt.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
}

func advisoryXactStart(t *testing.T, ctx context.Context, c fixture.Executor, q string) *advisorySessionWorker {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	w := &advisorySessionWorker{cancel: cancel, done: make(chan error, 1)}
	go func() {
		rows, err := c.QueryContext(ctx, q)
		if err == nil {
			var v any
			if !rows.Next() {
				err = rows.Err()
				if err == nil {
					err = fmt.Errorf("missing advisory result")
				}
			} else if err = rows.Scan(&v); err == nil && fixture.Normalize(v) != "" {
				err = fmt.Errorf("advisory result %#v", v)
			}
			if err == nil && rows.Next() {
				err = fmt.Errorf("extra advisory result")
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
		}
		w.done <- err
	}()
	t.Cleanup(func() {
		cancel()
		if !w.joined {
			select {
			case <-w.done:
				w.joined = true
			case <-time.After(10 * time.Second):
				t.Error("advisory worker did not join")
			}
		}
	})
	return w
}

type advisoryXactRow struct {
	Mode    string
	Granted bool
	PID     sql.NullInt64
	Waiting bool
}

func advisoryXactRows(t *testing.T, ctx context.Context, c fixture.Executor, k advisorySessionKey) []advisoryXactRow {
	t.Helper()
	rows, err := c.QueryContext(ctx, `SELECT mode,granted,pid,waitstart IS NOT NULL FROM pg_locks WHERE locktype='advisory' AND database=(SELECT oid FROM pg_database WHERE datname=current_database()) AND classid::bigint=$1 AND objid::bigint=$2 AND objsubid=$3`, int64(k.high), int64(k.low), k.space)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []advisoryXactRow
	for rows.Next() {
		var r advisoryXactRow
		if err := rows.Scan(&r.Mode, &r.Granted, &r.PID, &r.Waiting); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func advisoryXactWait(t *testing.T, p *fixture.Pair, observer fixture.Executor, waiter, holder int, k advisorySessionKey, shared bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	mode := "ExclusiveLock"
	if shared {
		mode = "ShareLock"
	}
	for {
		for _, r := range advisoryXactRows(t, ctx, observer, k) {
			if r.PID.Valid && r.PID.Int64 == int64(waiter) && !r.Granted && r.Waiting && r.Mode == mode {
				advisorySessionValue(t, ctx, observer, "SELECT $1=ANY(pg_blocking_pids($2))", "BOOL", true, holder, waiter)
				return
			}
		}
		if err := ctx.Err(); err != nil {
			t.Fatal("pending advisory request not observed", err)
		}
		runtime.Gosched()
	}
}

func advisoryXactModes(t *testing.T, p *fixture.Pair, c fixture.Executor, k advisorySessionKey, pid int, want ...string) {
	t.Helper()
	var got []string
	for _, r := range advisoryXactRows(t, p.Ctx, c, k) {
		if r.PID.Valid && r.PID.Int64 == int64(pid) {
			if !r.Granted || r.Waiting {
				t.Fatal("unexpected pending row", r)
			}
			got = append(got, r.Mode)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("advisory mode bag", got, "want", want)
	}
}

func advisoryXactProbe(t *testing.T, p *fixture.Pair, c fixture.Executor, k advisorySessionKey, shared, want bool) {
	t.Helper()
	advisorySessionValue(t, p.Ctx, c, k.call(advisoryXactName(false, shared, true)), "BOOL", want)
	if want {
		advisorySessionClear(t, p.Ctx, c)
	}
}
