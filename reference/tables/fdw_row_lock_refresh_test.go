package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func fdwRowLockRefreshMatrix(t *testing.T, changes ...string) {
	if len(changes) == 0 {
		changes = []string{"value", "predicate", "delete", "key", "replacement", "rollback"}
	}
	for _, prepared := range []bool{false, true} {
		for _, change := range changes {
			t.Run("late/"+fdwRowLockModeName(prepared)+"/"+change, func(t *testing.T) {
				fdwRowLockConfiguredScenario(t, true, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, locker *fdwSQLRowLocker, run func(fixture.Step)) {
					run(fixture.Exec("BEGIN ISOLATION LEVEL READ COMMITTED", 0))
					defer fdwRowLockRollback(t, c)
					mutate := func() {
						q := "UPDATE fdw_source.data SET label='changed',revision=2 WHERE rid=1"
						switch change {
						case "delete", "replacement":
							q = "DELETE FROM fdw_source.data WHERE rid=1"
						case "key":
							q = "UPDATE fdw_source.data SET id=9,label='changed',revision=2 WHERE rid=1"
						}
						if change == "rollback" {
							peer, err := source.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer peer.Close()
							fixture.LockExec(t, p.Ctx, peer, "BEGIN", 0)
							fixture.LockExec(t, p.Ctx, peer, q, 1)
							fixture.LockExec(t, p.Ctx, peer, "ROLLBACK", 0)
						} else {
							fixture.LockExec(t, p.Ctx, source, q, 1)
						}
						if change == "replacement" {
							fixture.LockExec(t, p.Ctx, source, "INSERT INTO fdw_source.data VALUES(1,999,1,1,'replacement',true)", 1)
						}
					}
					q := "SELECT id,label,flag FROM fdw_data WHERE id=1 FOR UPDATE"
					if change == "predicate" {
						q = "SELECT id,label,flag FROM fdw_data WHERE label='one' FOR UPDATE"
					}
					if b.PG {
						// Genuine early-locking postgres_fdw has no late capture callback.
						// Establish its real RR snapshot before the source mutation.
						fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, "SELECT id,label,flag FROM fdw_data WHERE id=1", prepared, [][]any{{int64(1), "one", true}}))
						mutate()
						if change == "rollback" {
							fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, q, prepared, [][]any{{int64(1), "one", true}}))
						} else {
							remote := "remote SQL command: SELECT id, label, flag FROM fdw_source.data WHERE ((id = 1)) FOR UPDATE"
							if change == "predicate" {
								remote = "remote SQL command: SELECT id, label, flag FROM fdw_source.data WHERE ((label = 'one')) FOR UPDATE"
							}
							fdwRowLockDiagnostic(t, fdwRowLockFailedQuery(p.Ctx, c, q, prepared), "40001", "could not serialize access due to concurrent update", remote)
						}
					} else {
						called := false
						locker.beforeRefetch = func(ctx context.Context, identity stuntgres.ForeignRowIdentity) error {
							if !called {
								called = true
								mutate()
							}
							return ctx.Err()
						}
						want := [][]any{{int64(1), "changed", true}}
						switch change {
						case "predicate", "delete", "replacement":
							want = [][]any{}
						case "key":
							want = [][]any{}
						case "rollback":
							want = [][]any{{int64(1), "one", true}}
						}
						// A changed visible key is still the original row. A broad
						// rid-independent predicate must project its NEW visible key.
						if change == "key" {
							q = "SELECT id,label,flag FROM fdw_data WHERE flag=true FOR UPDATE"
							want = [][]any{{int64(9), "changed", true}}
						}
						fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, q, prepared, want))
						if !called {
							t.Fatal("late refetch callback was not reached")
						}
						if change == "replacement" {
							// An identity mismatch must not lock the recycled key.
							fixture.LockExec(t, p.Ctx, source, "UPDATE fdw_source.data SET label='independent',revision=2 WHERE generation=999", 1)
						}
					}
				})
			})
		}
	}
}
