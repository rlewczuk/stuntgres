package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWRowLockAdmission(t *testing.T) {
	fdwRowLockAdmissionBoundaries(t)
	fdwRowLockAdmissionContracts(t)
	fdwRowLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, run func(fixture.Step)) {
		for _, prepared := range []bool{false, true} {
			for _, clause := range []string{"FOR KEY SHARE", "FOR SHARE", "FOR NO KEY UPDATE", "FOR UPDATE", "FOR KEY SHARE OF f FOR UPDATE OF f", "FOR UPDATE OF f NOWAIT", "FOR UPDATE OF f SKIP LOCKED"} {
				t.Run(fdwRowLockModeName(prepared)+"/"+clause, func(t *testing.T) {
					q := "SELECT id,label,flag FROM fdw_data f WHERE id=1 " + clause
					o := fdwRowLockRead(t, p.Ctx, c, q, prepared, [][]any{{int64(1), "one", true}})
					fdwRowLockClose(t, o)
					if prepared {
						// A real second Execute of the SAME prepared definition.
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						for range 2 {
							rows, err := stmt.QueryContext(p.Ctx)
							fdwRowLockValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, [][]any{{int64(1), "one", true}})
						}
					}
				})
			}
		}
	})
}

func TestReferenceFDWRowLockLifetime(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run("keyless/"+fdwRowLockModeName(prepared), func(t *testing.T) {
			fdwRowLockKeylessUpdate(t, prepared)
		})
		for _, strength := range []string{"KEY SHARE", "SHARE", "NO KEY UPDATE", "UPDATE"} {
			for _, end := range []string{"ROLLBACK", "COMMIT"} {
				t.Run(fdwRowLockModeName(prepared)+"/"+strength+"/"+end, func(t *testing.T) {
					fdwRowLockLifetime(t, prepared, strength, end)
				})
			}
		}
	}
}

func fdwRowLockLifetime(t *testing.T, prepared bool, strength, end string) {
	fdwRowLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, run func(fixture.Step)) {
		peer, ordinary := fixture.LockConnections(t, p.Ctx, source)
		waiter := 0
		if b.PG {
			waiter = fixture.LockPID(t, p.Ctx, peer)
		}
		run(fixture.Exec("BEGIN", 0))
		defer fdwRowLockRollback(t, c)
		o := fdwRowLockRead(t, p.Ctx, c, "SELECT id,label,flag FROM fdw_data WHERE id=1 FOR "+strength, prepared, [][]any{{int64(1), "one", true}})
		// A committed source SELECT and a different row's real UPDATE do not wait.
		var label string
		if err := ordinary.QueryRowContext(p.Ctx, "SELECT label FROM fdw_source.data WHERE rid=1").Scan(&label); err != nil || label != "one" {
			t.Fatal(label, err)
		}
		fixture.LockExec(t, p.Ctx, ordinary, "UPDATE fdw_source.data SET label='independent',revision=revision+1 WHERE rid=2", 1)
		finished := make(chan struct{})
		join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			defer close(finished)
			return fixture.RowExecute(ctx, peer, "DELETE FROM fdw_source.data WHERE rid=1", false)
		})
		observe := func() {
			if b.PG {
				fdwRowLockRemoteWait(t, p.Ctx, 0, waiter, 0)
			} else {
				fixture.NativeDeleteRowWait(t, p.Ctx, finished)
			}
		}
		observe()
		fdwRowLockClose(t, o)
		observe() // Neither Rows.Close nor Stmt.Close releases the remote lock.
		run(fixture.Exec(end, 0))
		result := join()
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		fixture.LockAffected(t, result.Result, 1)
		var count int64
		if err := ordinary.QueryRowContext(p.Ctx, "SELECT count(*) FROM fdw_source.data WHERE rid=1").Scan(&count); err != nil || count != 0 {
			t.Fatal(count, err)
		}
	})
}

func TestReferenceFDWRowLockSmoke(t *testing.T) {
	fdwRowLockRefreshMatrix(t, "predicate")
	for _, prepared := range []bool{false, true} {
		t.Run(fdwRowLockModeName(prepared), func(t *testing.T) {
			fdwRowLockLifetime(t, prepared, "UPDATE", "ROLLBACK")
			fdwRowLockBlockedCancel(t, prepared, "NOWAIT")
			fdwRowLockTwoPhase(t, prepared)
			fdwRowLockPersistence(t, prepared)
		})
	}
}
