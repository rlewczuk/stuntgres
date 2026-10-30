package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"runtime"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Shared source, ownership, cleanup and backend recording remain in the common
// FDW fixture. Only this family opts into genuine optional row locking.
func fdwRowLockScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *sql.DB, func(fixture.Step))) {
	t.Helper()
	fdwRowLockConfiguredScenario(t, false, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, _ *fdwSQLRowLocker, run func(fixture.Step)) {
		body(t, p, b, c, source, run)
	})
}

func fdwRowLockConfiguredScenario(t *testing.T, late bool, configure func(*fdwSQLRowLocker), body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *sql.DB, *fdwSQLRowLocker, func(fixture.Step))) {
	t.Helper()
	fdwConfiguredGraphScenario(t, true, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwSourceExec(t, p, b, reader, run, "CREATE SCHEMA fdw_source", 0)
		fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.data(rid integer PRIMARY KEY,generation integer NOT NULL,revision integer NOT NULL,id integer,label text,flag boolean)", 0)
		fdwSourceExec(t, p, b, reader, run, "INSERT INTO fdw_source.data VALUES(1,101,1,1,'one',true),(2,102,1,2,'two',false)", 2)
		fdwInstallLive(b, run)
		source := reader.source
		if b.PG {
			source = b.DB
		}
		body(t, p, b, c, source, reader.rowLocker, run)
	}, func(reader *fdwSQLReader) {
		reader.writer.lateIsolation = late
		reader.writer.incrementRevision = true
		reader.writer.orderedScans = true
		reader.rowLocker = &fdwSQLRowLocker{fdwSQLWriter: reader.writer}
		if configure != nil {
			configure(reader.rowLocker)
		}
	})
}

func fdwRowLockRollback(t *testing.T, c *sql.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Errorf("rowlock rollback: %v", err)
	}
}

// Retain both Rows and Stmt until the caller explicitly tests their closure.
// Even empty results must retain the exact public schema; identity stays hidden.
func fdwRowLockRead(t *testing.T, ctx context.Context, x fixture.Executor, q string, prepared bool, want [][]any) fixture.RelationOutcome {
	t.Helper()
	o := fixture.RowQuery(ctx, x, q, prepared)
	fdwRowLockValues(t, o, want)
	return o
}

func fdwRowLockValues(t *testing.T, o fixture.RelationOutcome, want [][]any) {
	t.Helper()
	if o.Rows != nil {
		t.Cleanup(func() { o.Rows.Close() })
	}
	if o.Stmt != nil {
		t.Cleanup(func() { o.Stmt.Close() })
	}
	if o.Err != nil {
		t.Fatal(o.Err)
	}
	if o.Rows == nil {
		t.Fatal("rowlock query returned no cursor")
	}
	names, err := o.Rows.Columns()
	if err != nil || !reflect.DeepEqual(names, []string{"id", "label", "flag"}) {
		t.Fatalf("rowlock columns %v: %v", names, err)
	}
	types, err := o.Rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"INT4", "TEXT", "BOOL"} {
		if types[i].DatabaseTypeName() != name {
			t.Fatalf("column %s type %s, want %s", names[i], types[i].DatabaseTypeName(), name)
		}
	}
	got := make([][]any, 0)
	for o.Rows.Next() {
		var id int64
		var label string
		var flag bool
		if err := o.Rows.Scan(&id, &label, &flag); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{id, label, flag})
	}
	if err := o.Rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rowlock values %#v, want %#v", got, want)
	}
}

func fdwRowLockClose(t *testing.T, o fixture.RelationOutcome) {
	t.Helper()
	if o.Rows != nil {
		if err := o.Rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if o.Stmt != nil {
		if err := o.Stmt.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// Observe the SOURCE backend, not the FDW frontend. The relation lock and
// blocker graph identify actual source SQL independently of operation timing.
// holder==0 finds the holder of a queued independent source DELETE;
// waiter==0 finds the remote FDW worker blocked behind a known source holder.
func fdwRowLockRemoteWait(t *testing.T, parent context.Context, frontend, waiter, holder int) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	for {
		var pid int
		q := `SELECT COALESCE((SELECT a.pid FROM pg_stat_activity a WHERE a.pid<>$1 AND a.wait_event_type='Lock' AND ($2=0 OR a.pid=$2) AND EXISTS(SELECT 1 FROM pg_locks l WHERE l.pid=a.pid AND l.relation='fdw_source.data'::regclass) AND ($3=0 OR $3=ANY(pg_blocking_pids(a.pid))) LIMIT 1),0)`
		if err := fixture.Admin.QueryRowContext(ctx, q, frontend, waiter, holder).Scan(&pid); err != nil {
			t.Fatal(err)
		}
		if pid != 0 {
			return pid
		}
		if ctx.Err() != nil {
			t.Fatal("source row lock was not observed", ctx.Err())
		}
		runtime.Gosched()
	}
}

func fdwRowLockDiagnostic(t *testing.T, o fixture.RelationOutcome, code, message string, remoteContext ...string) {
	t.Helper()
	if o.Rows != nil {
		defer o.Rows.Close()
	}
	if o.Stmt != nil {
		defer o.Stmt.Close()
	}
	if o.Rows != nil || o.Result != nil {
		t.Fatal("failed rowlock operation exposed successful rows/count", o.Err)
	}
	want := fixture.ControlsReferenceDiagnostic(code, message)
	if len(remoteContext) != 0 {
		want.Context = remoteContext[0]
	}
	fixture.AssertDiagnostic(t, o.Err, &want)
}

func fdwRowLockModeName(prepared bool) string {
	return fmt.Sprintf("prepared=%t", prepared)
}
