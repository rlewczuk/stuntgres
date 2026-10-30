package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWRowLockImplicit(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fdwRowLockModeName(prepared), func(t *testing.T) {
			fdwRowLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, run func(fixture.Step)) {
				o := fdwRowLockRead(t, p.Ctx, c, "SELECT id,label,flag FROM fdw_data WHERE id=1 FOR UPDATE", prepared, [][]any{{int64(1), "one", true}})
				// The eager implicit statement is complete even while its public
				// Rows/Stmt objects remain retained. No remote owner survives it.
				fixture.LockExec(t, p.Ctx, source, "DELETE FROM fdw_source.data WHERE rid=1", 1)
				fdwRowLockClose(t, o)
			})
		})
	}
}

func TestReferenceFDWRowLockSavepoint(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fdwRowLockModeName(prepared), func(t *testing.T) {
			fdwRowLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, run func(fixture.Step)) {
				peer, probe := fixture.LockConnections(t, p.Ctx, source)
				waiter := 0
				if b.PG {
					waiter = fixture.LockPID(t, p.Ctx, peer)
				}
				run(fixture.Exec("BEGIN", 0))
				defer fdwRowLockRollback(t, c)
				fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, "SELECT id,label,flag FROM fdw_data WHERE id=1 FOR UPDATE", prepared, [][]any{{int64(1), "one", true}}))
				run(fixture.Exec("SAVEPOINT suffix", 0))
				fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, "SELECT id,label,flag FROM fdw_data WHERE id=2 FOR UPDATE", prepared, [][]any{{int64(2), "two", false}}))
				run(fixture.Exec("ROLLBACK TO suffix", 0))
				// Actual remote savepoint rollback releases only its suffix lock.
				fixture.LockExec(t, p.Ctx, probe, "DELETE FROM fdw_source.data WHERE rid=2", 1)
				finished := make(chan struct{})
				join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(finished)
					return fixture.RowExecute(ctx, peer, "DELETE FROM fdw_source.data WHERE rid=1", false)
				})
				if b.PG {
					fdwRowLockRemoteWait(t, p.Ctx, 0, waiter, 0)
				} else {
					fixture.NativeDeleteRowWait(t, p.Ctx, finished)
				}
				run(fixture.Exec("ROLLBACK", 0))
				o := join()
				if o.Err != nil {
					t.Fatal(o.Err)
				}
				fixture.LockAffected(t, o.Result, 1)
			})
		})
	}
}

func fdwRowLockKeylessUpdate(t *testing.T, prepared bool) {
	fdwRowLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, run func(fixture.Step)) {
		peer, _ := fixture.LockConnections(t, p.Ctx, source)
		run(fixture.Exec("BEGIN", 0))
		defer fdwRowLockRollback(t, c)
		fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, "SELECT id,label,flag FROM fdw_data WHERE id=1 FOR KEY SHARE", prepared, [][]any{{int64(1), "one", true}}))
		q := "UPDATE fdw_source.data SET label='keyless',revision=revision+1 WHERE rid=1"
		if b.PG {
			// postgres_fdw promotes KEY SHARE to SHARE, which conflicts with
			// this real non-key UPDATE. Do not weaken its independent oracle.
			waiter := fixture.LockPID(t, p.Ctx, peer)
			join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
				return fixture.RowExecute(ctx, peer, q, false)
			})
			fdwRowLockRemoteWait(t, p.Ctx, 0, waiter, 0)
			run(fixture.Exec("ROLLBACK", 0))
			o := join()
			if o.Err != nil {
				t.Fatal(o.Err)
			}
			fixture.LockAffected(t, o.Result, 1)
		} else {
			fixture.LockExec(t, p.Ctx, peer, q, 1)
			run(fixture.Exec("ROLLBACK", 0))
		}
		var label string
		if err := source.QueryRowContext(p.Ctx, "SELECT label FROM fdw_source.data WHERE rid=1").Scan(&label); err != nil || label != "keyless" {
			t.Fatal(label, err)
		}
	})
}

func fdwRowLockOwnerClose(t *testing.T, prepared bool) {
	fdwRowLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, run func(fixture.Step)) {
		pool := sql.OpenDB(p.NativeConnector)
		if b.PG {
			pool.Close()
			var err error
			pool, err = sql.Open("postgres", fixture.DSN)
			if err != nil {
				t.Fatal(err)
			}
		}
		defer pool.Close()
		owner, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close()
		fixture.LockExec(t, p.Ctx, owner, "BEGIN", 0)
		fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, owner, "SELECT id,label,flag FROM fdw_data WHERE id=1 FOR UPDATE", prepared, [][]any{{int64(1), "one", true}}))
		fixture.LockExec(t, p.Ctx, owner, "UPDATE fdw_data SET label='uncommitted-owner' WHERE id=1", 1)
		peer, _ := fixture.LockConnections(t, p.Ctx, source)
		waiter := 0
		if b.PG {
			waiter = fixture.LockPID(t, p.Ctx, peer)
		}
		finished := make(chan struct{})
		join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			defer close(finished)
			return fixture.RowExecute(ctx, peer, "UPDATE fdw_source.data SET label=label||':released',revision=revision+1 WHERE rid=1", false)
		})
		if b.PG {
			fdwRowLockRemoteWait(t, p.Ctx, 0, waiter, 0)
		} else {
			fixture.RelationLockPending(t, p, b, 0, 0, 0, "", finished)
		}
		// Closing the separate pool first makes this the physical owner close,
		// not merely returning an active connection to an idle SQL pool.
		if err := pool.Close(); err != nil {
			t.Fatal(err)
		}
		if err := owner.Close(); err != nil {
			t.Fatal(err)
		}
		o := join()
		if o.Err != nil {
			t.Fatal(o.Err)
		}
		fixture.LockAffected(t, o.Result, 1)
		var label string
		if err := source.QueryRowContext(p.Ctx, "SELECT label FROM fdw_source.data WHERE rid=1").Scan(&label); err != nil || label != "one:released" {
			t.Fatal(label, err)
		}
	})
}
