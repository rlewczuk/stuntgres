package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Establish a real remote snapshot BEFORE an independent committed change.
// postgres_fdw maps local READ COMMITTED to remote REPEATABLE READ; a configured
// native oracle provider must implement that mapping explicitly, not fake it.
func TestReferenceFDWRowLockRefetch(t *testing.T) {
	fdwRowLockRefreshMatrix(t)
	for _, prepared := range []bool{false, true} {
		for _, isolation := range []string{"READ COMMITTED", "REPEATABLE READ", "SERIALIZABLE"} {
			for _, mutation := range []string{"update", "delete"} {
				t.Run(fdwRowLockModeName(prepared)+"/"+isolation+"/"+mutation, func(t *testing.T) {
					fdwRowLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, run func(fixture.Step)) {
						run(fixture.Exec("BEGIN ISOLATION LEVEL "+isolation, 0))
						defer fdwRowLockRollback(t, c)
						fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, "SELECT id,label,flag FROM fdw_data WHERE id=1", prepared, [][]any{{int64(1), "one", true}}))
						q := "UPDATE fdw_source.data SET label='changed',revision=2 WHERE rid=1"
						message := "could not serialize access due to concurrent update"
						// Both are SELECT FOR UPDATE refetches: native source
						// LatestQueryRow and pinned postgres_fdw report update
						// serialization conflicts even when the old row vanished.
						if mutation == "delete" {
							q = "DELETE FROM fdw_source.data WHERE rid=1"
						}
						fixture.LockExec(t, p.Ctx, source, q, 1)
						o := fdwRowLockFailedQuery(p.Ctx, c, "SELECT id,label,flag FROM fdw_data WHERE id=1 FOR UPDATE", prepared)
						remoteContext := ""
						if b.PG {
							remoteContext = "remote SQL command: SELECT id, label, flag FROM fdw_source.data WHERE ((id = 1)) FOR UPDATE"
						}
						fdwRowLockDiagnostic(t, o, "40001", message, remoteContext)
					})
				})
			}
		}
	}
}

func TestReferenceFDWRowLockTransactions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fdwRowLockModeName(prepared), func(t *testing.T) {
			fdwRowLockTwoPhase(t, prepared)
			fdwRowLockOwnerChain(t, prepared)
			fdwRowLockOwnerClose(t, prepared)
		})
	}
}

func fdwRowLockTwoPhase(t *testing.T, prepared bool) {
	fdwRowLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, run func(fixture.Step)) {
		run(fixture.Exec("BEGIN", 0))
		defer fdwRowLockRollback(t, c)
		fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, "SELECT id,label,flag FROM fdw_data WHERE id=1 FOR UPDATE", prepared, [][]any{{int64(1), "one", true}}))
		o := fixture.RowExecute(p.Ctx, c, "PREPARE TRANSACTION 'fdw_row_lock_unsupported'", prepared)
		message := "cannot PREPARE a transaction that has operated on foreign tables"
		if b.PG {
			message = "cannot PREPARE a transaction that has operated on postgres_fdw foreign tables"
		}
		fdwRowLockDiagnostic(t, o, "0A000", message)
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT gid FROM pg_prepared_xacts WHERE gid='fdw_row_lock_unsupported'", []string{"gid"}, []string{"TEXT"}, [][]any{}))
		fixture.LockExec(t, p.Ctx, source, "DELETE FROM fdw_source.data WHERE rid=1", 1)
	})
}

func fdwRowLockOwnerChain(t *testing.T, prepared bool) {
	fdwRowLockConfiguredScenario(t, false, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, locker *fdwSQLRowLocker, run func(fixture.Step)) {
		run(fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
		defer fdwRowLockRollback(t, c)
		before := locker.transactions
		fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, "SELECT id,label,flag FROM fdw_data WHERE id=1", prepared, [][]any{{int64(1), "one", true}}))
		fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, "SELECT id,label,flag FROM fdw_data WHERE id=1 FOR UPDATE", prepared, [][]any{{int64(1), "one", true}}))
		run(fixture.Exec("UPDATE fdw_data SET label='owned' WHERE id=1", 1))
		fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, "SELECT id,label,flag FROM fdw_data WHERE id=1 FOR UPDATE", prepared, [][]any{{int64(1), "owned", true}}))
		if !b.PG && locker.transactions-before != 1 {
			t.Fatal("one owner enlisted duplicate transactions", locker.transactions-before)
		}
		run(fixture.Exec("COMMIT AND CHAIN", 0))
		// The old owner's remote lock is gone, but the chained owner keeps
		// its resolved isolation. A fresh source transaction mutates freely.
		fixture.LockExec(t, p.Ctx, source, "UPDATE fdw_source.data SET label='chained',revision=revision+1 WHERE rid=1", 1)
		fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, "SELECT id,label,flag FROM fdw_data WHERE id=1 FOR UPDATE", prepared, [][]any{{int64(1), "chained", true}}))
		run(fixture.Exec("ROLLBACK AND CHAIN", 0))
		fixture.LockExec(t, p.Ctx, source, "DELETE FROM fdw_source.data WHERE rid=1", 1)
		run(fixture.Exec("ROLLBACK", 0))
	})
}

// Drivers may report execution errors either from QueryContext or cursor Next.
// Preserve the real error and never reinterpret successful rows as an error.
func fdwRowLockFailedQuery(ctx context.Context, x fixture.Executor, q string, prepared bool) fixture.RelationOutcome {
	return fdwRowLockQueryFailure(fixture.RowQuery(ctx, x, q, prepared))
}

// Also accepts executions of an already prepared statement without closing it.
func fdwRowLockQueryFailure(o fixture.RelationOutcome) fixture.RelationOutcome {
	if o.Err == nil && o.Rows != nil {
		for o.Rows.Next() {
		}
		o.Err = o.Rows.Err()
		if err := o.Rows.Close(); o.Err == nil {
			o.Err = err
		}
		if o.Err != nil {
			o.Rows = nil
		}
	}
	return o
}
