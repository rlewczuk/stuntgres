package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"runtime"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

type advisorySessionKey struct {
	args      string
	high, low uint32
	space     int
}

var advisorySessionKeys = []advisorySessionKey{
	{"4294967298::bigint", 1, 2, 1},
	{"1::integer,2::integer", 1, 2, 2},
}

func (k advisorySessionKey) call(name string) string { return "SELECT " + name + "(" + k.args + ")" }

func advisorySessionConn(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil && err != sql.ErrConnDone {
			t.Error(err)
		}
	})
	return c
}

func advisorySessionValue(t *testing.T, ctx context.Context, c fixture.Executor, q string, typ string, want any, args ...any) {
	t.Helper()
	rows, err := c.QueryContext(ctx, q, args...)
	if err != nil {
		if rows != nil {
			rows.Close()
		}
		t.Fatal(q, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil || len(cols) != 1 {
		t.Fatal(q, cols, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != typ {
		t.Fatal(q, types, err)
	}
	if !rows.Next() {
		t.Fatal(q, "missing row", rows.Err())
	}
	var raw any
	if err := rows.Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fixture.Normalize(raw), want) {
		t.Fatalf("%s raw=%#v normalized=%#v want=%#v", q, raw, fixture.Normalize(raw), want)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(q, "extra row/error", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func advisorySessionExec(t *testing.T, ctx context.Context, c fixture.Executor, q string, count int64, args ...any) {
	t.Helper()
	r, err := c.ExecContext(ctx, q, args...)
	if err != nil {
		t.Fatal(q, err)
	}
	n, err := r.RowsAffected()
	if err != nil || n != count {
		t.Fatal(q, n, err)
	}
	if _, err := r.LastInsertId(); err == nil {
		t.Fatal("LastInsertId supported", q)
	}
}

func advisorySessionError(t *testing.T, ctx context.Context, c fixture.Executor, q string, want fixture.Diagnostic) {
	t.Helper()
	rows, err := c.QueryContext(ctx, q)
	if rows != nil {
		// pq can report an execution error after opening a streaming cursor.
		for rows.Next() {
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
	}
	fixture.AssertDiagnostic(t, err, &want)
}

func advisorySessionClear(t *testing.T, ctx context.Context, c fixture.Executor) {
	t.Helper()
	advisorySessionValue(t, ctx, c, "SELECT pg_advisory_unlock_all()", "VOID", "")
}

func advisorySessionWait(t *testing.T, ctx context.Context, waiter, holder int, k advisorySessionKey, shared bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	mode := "ExclusiveLock"
	if shared {
		mode = "ShareLock"
	}
	for {
		var pending bool
		err := fixture.Admin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND database=(SELECT oid FROM pg_database WHERE datname=current_database()) AND classid::bigint=$3 AND objid::bigint=$4 AND objsubid=$5 AND mode=$6 AND NOT granted) AND $2=ANY(pg_blocking_pids($1))`, waiter, holder, int64(k.high), int64(k.low), k.space, mode).Scan(&pending)
		if err != nil {
			t.Fatal("advisory pending observation", err)
		}
		if pending {
			return
		}
		runtime.Gosched()
	}
}

type advisorySessionWorker struct {
	cancel context.CancelFunc
	done   chan error
	joined bool
}

// Workers never call testing methods. Every failure path cancels and joins
// before pool/schema cleanup; buffered completion cannot strand its producer.
func advisorySessionStart(t *testing.T, ctx context.Context, c *sql.Conn, q string) *advisorySessionWorker {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	w := &advisorySessionWorker{cancel: cancel, done: make(chan error, 1)}
	go func() {
		var value any
		err := c.QueryRowContext(ctx, q).Scan(&value)
		if err == nil && fixture.Normalize(value) != "" {
			err = fmt.Errorf("acquire returned %#v", value)
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
				t.Error("advisory worker cleanup failed to join")
			}
		}
	})
	return w
}

func (w *advisorySessionWorker) await(t *testing.T) error {
	t.Helper()
	select {
	case err := <-w.done:
		w.joined = true
		w.cancel()
		return err
	case <-time.After(10 * time.Second):
		w.cancel()
		t.Fatal("advisory worker failed to join")
		return nil
	}
}

func advisorySessionTimeout(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func advisorySessionMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 {
		t.Fatal("advisory descriptor", types, err)
	}
	typ := types[0]
	wantScan := reflect.TypeFor[bool]()
	switch typ.DatabaseTypeName() {
	case "VOID":
		wantScan = reflect.TypeFor[string]()
		if pg {
			wantScan = reflect.TypeFor[any]()
		}
	case "BOOL":
	default:
		t.Fatal("unexpected advisory result type", typ.DatabaseTypeName())
	}
	if typ.ScanType() != wantScan {
		t.Fatal("advisory ScanType", typ.ScanType(), wantScan)
	}
	if nullable, known := typ.Nullable(); nullable || known {
		t.Fatal("computed advisory nullable", nullable, known)
	}
	if length, known := typ.Length(); length != 0 || known {
		t.Fatal("advisory length", length, known)
	}
	if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
		t.Fatal("advisory decimal size", precision, scale, known)
	}
}
