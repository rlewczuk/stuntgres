package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWTableHierarchyLockRecheck(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, predicate := range []bool{false, true} {
			t.Run(fmt.Sprintf("local/%s/predicate=%t", fdwRowLockModeName(prepared), predicate), func(t *testing.T) {
				fdwRowLockConfiguredScenario(t, false, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, _ *fdwSQLRowLocker, run func(fixture.Step)) {
					fdwHierarchyLockEmptySource(t, p, source)
					run(fixture.Exec("CREATE TABLE fdw_hlock_parent(id integer,label text,flag boolean)", 0))
					run(fixture.Exec("CREATE TABLE fdw_hlock_child(label text,flag boolean,id integer)", 0))
					run(fixture.Exec("ALTER TABLE fdw_hlock_child INHERIT fdw_hlock_parent", 0))
					run(fixture.Exec("CREATE FOREIGN TABLE fdw_hlock_empty() INHERITS(fdw_hlock_parent) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'hlock_empty')", 0))
					run(fixture.Exec("INSERT INTO fdw_hlock_child(id,label,flag) VALUES(1,'old',true)", 1))
					holder, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
					fixture.LockExec(t, p.Ctx, holder, "UPDATE fdw_hlock_child SET label='changed' WHERE id=1", 1)
					run(fixture.Exec("BEGIN ISOLATION LEVEL READ COMMITTED", 0))
					t.Cleanup(func() { fdwRowLockRollback(t, c) })
					query := "SELECT id,label,flag,tableoid='fdw_hlock_child'::regclass AS physical FROM fdw_hlock_parent WHERE id=1"
					if predicate {
						query += " AND label='old'"
					}
					query += " FOR UPDATE"
					// Fail immediately on the broken adapter before starting any
					// observer for an event that it cannot produce. This also proves
					// the blocker is the child's real predecessor, not the parent.
					preflight := fixture.ErrorDiagnostic(query+" NOWAIT", fdwDiagnostic("55P03", `could not obtain lock on row in relation "fdw_hlock_child"`))
					preflight.SelectRows, preflight.Prepared, preflight.ErrorStage = true, prepared, "execute"
					run(preflight)
					run(fixture.Exec("ROLLBACK", 0))
					run(fixture.Exec("BEGIN ISOLATION LEVEL READ COMMITTED", 0))
					waiterPID, holderPID := 0, 0
					if b.PG {
						waiterPID = fixture.LockPID(t, p.Ctx, c)
						holderPID = fixture.LockPID(t, p.Ctx, holder)
					}
					finished := make(chan struct{})
					join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(finished)
						return fixture.RowQuery(ctx, c, query, prepared)
					})
					fdwHierarchyLockWait(t, p, b, waiterPID, holderPID, finished)
					fixture.LockExec(t, p.Ctx, holder, "COMMIT", 0)
					outcome := join()
					want := [][]any{{int64(1), "changed", true, true}}
					if predicate {
						want = nil // The actual replacement no longer satisfies label='old'.
					}
					fdwHierarchyLockRows(t, outcome, fixture.Select(query, []string{"id", "label", "flag", "physical"}, []string{"INT4", "TEXT", "BOOL", "BOOL"}, want))
					fdwRowLockClose(t, outcome)
					probe := "SELECT id FROM ONLY fdw_hlock_child WHERE id=1 FOR UPDATE NOWAIT"
					if !predicate {
						fdwHierarchyLockProbe(t, p, b, peer, probe, "id", "fdw_hlock_child", 1, true)
					}
					run(fixture.Exec("ROLLBACK", 0))
					fdwHierarchyLockProbe(t, p, b, peer, probe, "id", "fdw_hlock_child", 1, false)
					run(fixture.Select("SELECT id,label,flag,tableoid='fdw_hlock_child'::regclass AS physical FROM fdw_hlock_parent", []string{"id", "label", "flag", "physical"}, []string{"INT4", "TEXT", "BOOL", "BOOL"}, [][]any{{int64(1), "changed", true, true}}))
					fdwHierarchyLockEmptyRows(t, p, b, source)
				})
			})
		}
		for _, partition := range []bool{false, true} {
			for _, matching := range []bool{false, true} {
				t.Run(fmt.Sprintf("foreign/%s/partition=%t/matching=%t", fdwRowLockModeName(prepared), partition, matching), func(t *testing.T) {
					fdwRowLockConfiguredScenario(t, true, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, locker *fdwSQLRowLocker, run func(fixture.Step)) {
						fdwHierarchyLockForeignParent(run, partition)
						run(fixture.Exec("BEGIN ISOLATION LEVEL READ COMMITTED", 0))
						t.Cleanup(func() { fdwRowLockRollback(t, c) })
						mutations := 0
						newID := int64(1)
						if !matching {
							newID = 9
						}
						mutate := func() {
							fixture.LockExec(t, p.Ctx, source, fmt.Sprintf("UPDATE fdw_source.data SET id=%d,label='changed',revision=2 WHERE rid=1", newID), 1)
							mutations++
						}
						query := "SELECT id,label,flag FROM fdw_hlock_parent WHERE id=1 FOR UPDATE"
						if b.PG {
							// The real postgres_fdw participant has an RR source
							// snapshot, even though the frontend is READ COMMITTED.
							// Establish it before the independent committed update.
							run(fdwRows("SELECT id,label,flag FROM fdw_hlock_parent WHERE id=1", [][]any{{int64(1), "one", true}}))
							mutate()
						} else {
							locker.beforeRefetch = func(ctx context.Context, identity stuntgres.ForeignRowIdentity) error {
								if len(identity) != 3 || identity[0] != int64(1) || identity[1] != int64(101) || identity[2] != int64(1) {
									return errors.New("hierarchy refetch lost original rid/generation/revision")
								}
								if mutations == 0 {
									mutate()
								}
								return ctx.Err()
							}
							defer func() { locker.beforeRefetch = nil }()
						}
						outcome := fixture.RowQuery(p.Ctx, c, query, prepared)
						if b.PG {
							// This is the established provider-specific error, not a
							// parity-derived fallback. Root checks the inherited-plan
							// remote Context on the pinned 18.6 oracle checkpoint.
							outcome = fdwRowLockQueryFailure(outcome)
							fdwRowLockDiagnostic(t, outcome, "40001", "could not serialize access due to concurrent update", "remote SQL command: SELECT id, label, flag, ctid FROM fdw_source.data WHERE ((id = 1)) FOR UPDATE")
						} else {
							want := [][]any{{int64(1), "changed", true}}
							if !matching {
								want = [][]any{}
							}
							fdwRowLockValues(t, outcome, want)
							fdwRowLockClose(t, outcome)
						}
						if mutations != 1 {
							t.Fatalf("source mutation count %d, want 1 after original identity capture", mutations)
						}
						locker.beforeRefetch = nil
						run(fixture.Exec("ROLLBACK", 0))
						fixture.RunStep(t, p.Ctx, source, b.Recorder, fixture.Select("SELECT rid,generation,revision,id,label,flag FROM fdw_source.data ORDER BY rid", []string{"rid", "generation", "revision", "id", "label", "flag"}, []string{"INT4", "INT4", "INT4", "INT4", "TEXT", "BOOL"}, [][]any{{int64(1), int64(101), int64(2), newID, "changed", true}, {int64(2), int64(102), int64(1), int64(2), "two", false}}))
					})
				})
			}
		}
	}
}
