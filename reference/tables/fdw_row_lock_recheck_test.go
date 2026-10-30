package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWRowLockRecheck(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, shape := range []string{"derived", "view"} {
			t.Run("changed-projection/"+fdwRowLockModeName(prepared)+"/"+shape, func(t *testing.T) {
				fdwRowLockChangedProjection(t, prepared, shape)
			})
		}
		for _, reference := range []bool{false, true} {
			for _, shape := range []string{"table", "derived", "view"} {
				t.Run(fdwRowLockModeName(prepared)+"/reference="+fdwRowLockModeName(reference)+"/"+shape, func(t *testing.T) {
					fdwRowLockConfiguredScenario(t, true, func(w *fdwSQLRowLocker) {
						if reference {
							w.sibling = stuntgres.ForeignRowMarkReference
						}
					}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, locker *fdwSQLRowLocker, run func(fixture.Step)) {
						run(fixture.Exec("CREATE TABLE fdw_recheck_local(id integer PRIMARY KEY,k integer)", 0))
						run(fixture.Exec("INSERT INTO fdw_recheck_local VALUES(1,1)", 1))
						run(fixture.Exec("CREATE VIEW fdw_recheck_view AS SELECT id,label,flag FROM fdw_data", 0))
						local, _ := fixture.LockConnections(t, p.Ctx, b.DB)
						fixture.LockExec(t, p.Ctx, local, "BEGIN", 0)
						fixture.LockExec(t, p.Ctx, local, "UPDATE fdw_recheck_local SET k=2 WHERE id=1", 1)
						run(fixture.Exec("BEGIN", 0))
						defer fdwRowLockRollback(t, c)
						from := "fdw_data"
						if shape == "derived" {
							from = "(SELECT id,label,flag FROM fdw_data)"
						}
						if shape == "view" {
							from = "fdw_recheck_view"
						}
						q := "SELECT f.id,f.label,f.flag FROM fdw_recheck_local l JOIN " + from + " f ON l.k=f.id WHERE l.id=1 FOR UPDATE OF l"
						// Capture backend identities before lending c to the worker.
						// database/sql serializes another query on that connection
						// behind the blocked statement, including pg_backend_pid().
						waiter, holder := 0, 0
						if b.PG {
							waiter = fixture.LockPID(t, p.Ctx, c)
							holder = fixture.LockPID(t, p.Ctx, local)
						}
						finished := make(chan struct{})
						join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							defer close(finished)
							return fixture.RowQuery(ctx, c, q, prepared)
						})
						if b.PG {
							fixture.RowWait(t, p.Ctx, waiter, holder)
						} else {
							fixture.RelationLockPending(t, p, b, 0, 0, 0, "", finished)
						}
						fixture.LockExec(t, p.Ctx, source, "UPDATE fdw_source.data SET id=2,label='changed',revision=2 WHERE rid=1", 1)
						fixture.LockExec(t, p.Ctx, local, "COMMIT", 0)
						want := [][]any{}
						if !b.PG && reference {
							want = [][]any{{int64(2), "changed", true}}
						}
						o := join()
						fdwRowLockValues(t, o, want)
						fdwRowLockClose(t, o)
						run(fixture.Exec("ROLLBACK", 0))
						// An absent outer occurrence must stay NULL, not be refetched
						// as some existing remote row or discarded by an inner qual.
						step := fixture.Select("SELECT f.id,f.label,f.flag FROM fdw_recheck_local l LEFT JOIN fdw_data f ON f.id=99 FOR UPDATE OF l", []string{"id", "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{nil, nil, nil}})
						step.Prepared = prepared
						run(step)
					})
				})
			}
		}
		for _, result := range []string{"true", "false", "error"} {
			t.Run("provider/"+fdwRowLockModeName(prepared)+"/"+result, func(t *testing.T) {
				fdwRowLockProviderRecheck(t, prepared, result)
			})
		}
	}
}

func fdwRowLockProviderRecheck(t *testing.T, prepared bool, result string) {
	failure := errors.New("source predicate rejected changed tuple")
	fdwRowLockConfiguredScenario(t, true, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, locker *fdwSQLRowLocker, run func(fixture.Step)) {
		q := "SELECT id,label,flag FROM fdw_data WHERE id=1 AND flag=true FOR UPDATE"
		mutate := func() {
			flag := "true"
			if result == "false" {
				flag = "false"
			}
			fixture.LockExec(t, p.Ctx, source, "UPDATE fdw_source.data SET label='changed',flag="+flag+",revision=2 WHERE rid=1", 1)
		}
		if b.PG {
			mutate()
			if result == "error" {
				q = "SELECT id/(id-1) AS id,label,flag FROM (SELECT id,label,flag FROM fdw_data WHERE id=1 FOR UPDATE) s"
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
			locker.recheck = func(ctx context.Context, row stuntgres.ForeignRow) (bool, error) {
				if result == "error" {
					return false, failure
				}
				return row[2] == true, ctx.Err()
			}
		}
		if result == "error" {
			o := fdwRowLockFailedQuery(p.Ctx, c, q, prepared)
			if b.PG {
				fdwRowLockDiagnostic(t, o, "22012", "division by zero")
			} else if o.Rows != nil || o.Result != nil || !errors.Is(o.Err, failure) {
				t.Fatalf("provider predicate error: %#v", o)
			}
			if o.Stmt != nil {
				o.Stmt.Close()
			}
		} else {
			want := [][]any{{int64(1), "changed", true}}
			if result == "false" {
				want = [][]any{}
			}
			fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, q, prepared, want))
		}
	})
}

// Both query levels have locking frontiers. The inner volatile projection must
// observe the qualifying replacement once; carrying that settled replacement
// through the outer frontier must not project it again.
func fdwRowLockChangedProjection(t *testing.T, prepared bool, shape string) {
	fdwRowLockConfiguredScenario(t, true, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, locker *fdwSQLRowLocker, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE fdw_changed_projection_calls(label text)", 0))
		run(fixture.Exec("CREATE SEQUENCE fdw_changed_projection_count", 0))
		run(fixture.Exec("CREATE FUNCTION fdw_changed_label(value text) RETURNS text LANGUAGE plpgsql VOLATILE AS $$BEGIN PERFORM nextval('fdw_changed_projection_count'); INSERT INTO fdw_changed_projection_calls VALUES(value); RETURN value; END$$", 0))
		inner := "SELECT id,fdw_changed_label(label) AS label,flag FROM fdw_data WHERE id=1 FOR UPDATE"
		run(fixture.Exec("CREATE VIEW fdw_changed_projection AS "+inner, 0))
		from := "(" + inner + ")"
		if shape == "view" {
			from = "fdw_changed_projection"
		}
		q := "SELECT id,label,flag FROM " + from + " s FOR UPDATE"
		var stmt *sql.Stmt
		if prepared {
			var err error
			stmt, err = c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
		}
		executions := 1
		if prepared {
			executions = 2 // The second execution reuses the same prepared definition.
		}
		for range executions {
			fixture.LockExec(t, p.Ctx, source, "UPDATE fdw_source.data SET label='one',revision=1 WHERE rid=1", 1)
			run(fixture.Exec("ALTER SEQUENCE fdw_changed_projection_count RESTART WITH 1", 0))
			run(fixture.Exec("BEGIN", 0))
			defer fdwRowLockRollback(t, c)
			mutations := 0
			mutate := func() {
				fixture.LockExec(t, p.Ctx, source, "UPDATE fdw_source.data SET label='changed',revision=2 WHERE rid=1", 1)
				mutations++
			}
			if b.PG {
				// postgres_fdw establishes an RR snapshot before early locking.
				// A later genuine source update cannot become a latest-row success.
				fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, "SELECT id,label,flag FROM fdw_data WHERE id=1", false, [][]any{{int64(1), "one", true}}))
				mutate()
			} else {
				locker.beforeRefetch = func(ctx context.Context, identity stuntgres.ForeignRowIdentity) error {
					if mutations == 0 {
						// The supplied identity is the pre-mutation source revision,
						// proving this barrier follows capture, not just query start.
						if len(identity) != 3 || identity[0] != int64(1) || identity[2] != int64(1) {
							return errors.New("changed projection did not capture the original source identity")
						}
						mutate()
					}
					return ctx.Err()
				}
			}
			var o fixture.RelationOutcome
			if stmt != nil {
				o.Rows, o.Err = stmt.QueryContext(p.Ctx)
			} else {
				o = fixture.RowQuery(p.Ctx, c, q, false)
			}
			if b.PG {
				o = fdwRowLockQueryFailure(o)
				fdwRowLockDiagnostic(t, o, "40001", "could not serialize access due to concurrent update", "remote SQL command: SELECT id, label, flag FROM fdw_source.data WHERE ((id = 1)) FOR UPDATE")
			} else {
				fdwRowLockValues(t, o, [][]any{{int64(1), "changed", true}})
				fdwRowLockClose(t, o)
				// Exact bag and value establish one invocation, not merely one
				// returned row or a counter that ignores stale evaluations.
				run(fixture.Select("SELECT label FROM fdw_changed_projection_calls", []string{"label"}, []string{"TEXT"}, [][]any{{"changed"}}))
			}
			if mutations != 1 {
				t.Fatalf("source mutation count %d, want 1", mutations)
			}
			locker.beforeRefetch = nil
			run(fixture.Exec("ROLLBACK", 0))
			// Sequence advancement survives rollback, so PostgreSQL's error
			// cannot hide a projection invocation by undoing the audit INSERT.
			run(fixture.Select("SELECT last_value,is_called FROM fdw_changed_projection_count", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), !b.PG}}))
			run(fixture.Select("SELECT label FROM fdw_changed_projection_calls", []string{"label"}, []string{"TEXT"}, [][]any{}))
			rows, err := source.QueryContext(p.Ctx, "SELECT id,label,flag FROM fdw_source.data ORDER BY id")
			fdwRowLockValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, [][]any{{int64(1), "changed", true}, {int64(2), "two", false}})
		}
	})
}
