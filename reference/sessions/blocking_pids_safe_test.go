package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"stuntgres/reference/fixture"
	"testing"
)

func blockingSeedRows(t *testing.T, rows *sql.Rows) {
	t.Helper()
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].Name() != "v" || types[0].DatabaseTypeName() != "INT4" {
		t.Fatal("safe rows metadata", types, err)
	}
	var value int64
	if !rows.Next() {
		t.Fatal("missing safe row", rows.Err())
	}
	if err := rows.Scan(&value); err != nil || value != 11 {
		t.Fatal(value, err)
	}
	if rows.Next() {
		t.Fatal("extra safe row")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func blockingSafeOperation(t *testing.T, p *fixture.Pair, reader *sql.Conn, prepare bool) func() fixture.RelationOutcome {
	t.Helper()
	return fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
		if prepare {
			s, err := reader.PrepareContext(ctx, "SELECT v FROM blocking_safe")
			return fixture.RelationOutcome{Stmt: s, Err: err}
		}
		r, err := reader.QueryContext(ctx, "SELECT v FROM blocking_safe")
		return fixture.RelationOutcome{Rows: r, Err: err}
	})
}

func blockingSafeDone(t *testing.T, p *fixture.Pair, await func() fixture.RelationOutcome, prepare bool) {
	t.Helper()
	o := await()
	if o.Err != nil {
		t.Fatal(o.Err)
	}
	if prepare {
		if o.Stmt == nil {
			t.Fatal("missing prepared safe handle")
		}
		defer o.Stmt.Close()
		rows, err := o.Stmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		blockingSeedRows(t, rows)
	} else {
		if o.Rows == nil {
			t.Fatal("missing safe cursor")
		}
		blockingSeedRows(t, o.Rows)
	}
}

func TestReferenceBlockingPidsSafeSnapshot(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		for _, release := range []string{"COMMIT", "ROLLBACK", "prepared"} {
			t.Run(map[bool]string{false: "query/", true: "prepare/"}[prepare]+release, func(t *testing.T) {
				blockingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					writer, wid := blockingConn(t, p, b)
					reader, rid := blockingConn(t, p, b)
					observer, _ := blockingConn(t, p, b)
					namespaceExec(t, p, b, writer, "CREATE TABLE blocking_safe(v INT)")
					fixture.RunStep(t, p.Ctx, writer, b.Recorder, fixture.Exec("INSERT INTO blocking_safe VALUES(11)", 1))
					namespaceExec(t, p, b, writer, "BEGIN ISOLATION LEVEL SERIALIZABLE")
					fixture.PreparedReferenceRows(t, p, b, writer, "SELECT v FROM blocking_safe", 11)
					namespaceExec(t, p, b, reader, "BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE")
					// BEGIN alone has not acquired a safe snapshot.
					blockingWant(t, p, b, observer, "pg_safe_snapshot_blocking_pids", rid)
					// Cleanup is registered before the worker, so worker cancellation/join
					// precedes prepared-owner cleanup on every assertion-failure path.
					if release == "prepared" {
						fixture.PreparedReferenceCleanup(t, b, "blocking_safe_writer")
					}
					await := blockingSafeOperation(t, p, reader, prepare)
					blockingWait(t, p, b, observer, "pg_safe_snapshot_blocking_pids", rid, wid)
					blockingWant(t, p, b, observer, "pg_blocking_pids", rid)
					if release == "prepared" {
						namespaceExec(t, p, b, writer, "PREPARE TRANSACTION 'blocking_safe_writer'")
						blockingWait(t, p, b, observer, "pg_safe_snapshot_blocking_pids", rid, 0)
						if err := writer.Raw(func(any) error { return driver.ErrBadConn }); err != driver.ErrBadConn {
							t.Fatal(err)
						}
						blockingWant(t, p, b, observer, "pg_safe_snapshot_blocking_pids", rid, 0)
						namespaceExec(t, p, b, observer, "ROLLBACK PREPARED 'blocking_safe_writer'")
					} else {
						namespaceExec(t, p, b, writer, release)
					}
					blockingSafeDone(t, p, await, prepare)
					blockingWant(t, p, b, observer, "pg_safe_snapshot_blocking_pids", rid)
					blockingWant(t, p, b, observer, "pg_blocking_pids", rid)
					namespaceExec(t, p, b, reader, "ROLLBACK")
				})
			})
		}
	}
	t.Run("not_actual_safe_waits", func(t *testing.T) {
		blockingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			writer, _ := blockingConn(t, p, b)
			reader, rid := blockingConn(t, p, b)
			observer, _ := blockingConn(t, p, b)
			namespaceExec(t, p, b, writer, "CREATE TABLE blocking_safe(v INT)")
			fixture.RunStep(t, p.Ctx, writer, b.Recorder, fixture.Exec("INSERT INTO blocking_safe VALUES(11)", 1))
			namespaceExec(t, p, b, writer, "BEGIN ISOLATION LEVEL SERIALIZABLE")
			fixture.PreparedReferenceRows(t, p, b, writer, "SELECT v FROM blocking_safe", 11)
			for _, begin := range []string{"BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY NOT DEFERRABLE", "BEGIN ISOLATION LEVEL READ COMMITTED READ ONLY", "BEGIN ISOLATION LEVEL SERIALIZABLE READ WRITE DEFERRABLE"} {
				namespaceExec(t, p, b, reader, begin)
				fixture.PreparedReferenceRows(t, p, b, reader, "SELECT v FROM blocking_safe", 11)
				blockingWant(t, p, b, observer, "pg_safe_snapshot_blocking_pids", rid)
				blockingWant(t, p, b, observer, "pg_blocking_pids", rid)
				namespaceExec(t, p, b, reader, "ROLLBACK")
			}
			namespaceExec(t, p, b, writer, "ROLLBACK")
		})
	})
}
