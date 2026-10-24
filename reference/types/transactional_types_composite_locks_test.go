package reference_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalTypesCompositeIdentityLocks(t *testing.T) {
	for _, move := range []bool{false, true} {
		for _, savepoint := range []bool{false, true} {
			t.Run(fmt.Sprintf("move=%v/savepoint=%v", move, savepoint), func(t *testing.T) {
				fixture.SchemaReferenceCleanup(t, "tt_composite_dst")
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						owner, editor := fixture.LockConnections(t, p.Ctx, b.DB)
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, owner, b.Recorder, s) }
						for _, q := range []string{"CREATE SCHEMA tt_composite_dst", "CREATE TYPE tt_lock_pair AS(id integer)", "CREATE TABLE tt_lock_typed OF tt_lock_pair"} {
							run(fixture.Exec(q, 0))
						}
						run(fixture.Exec("INSERT INTO tt_lock_typed VALUES(1)", 1))
						query := "ALTER TYPE tt_lock_pair RENAME TO tt_lock_renamed"
						if move {
							query = "ALTER TYPE tt_lock_pair SET SCHEMA tt_composite_dst"
						}
						held, err := owner.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
						ownerPID, editorPID := 0, 0
						var backing int64
						var sourceCTID string
						if b.PG {
							ownerPID, editorPID = fixture.RowPIDs(t, p.Ctx, owner, editor)
							backing = fixture.LockOID(t, p.Ctx, "tt_lock_pair")
							if err := owner.QueryRowContext(p.Ctx, "SELECT ctid::text FROM pg_class WHERE oid='tt_lock_pair'::regclass").Scan(&sourceCTID); err != nil {
								t.Fatal(err)
							}
						}
						// Attribute evolution owns the actual composite backing relation.
						fixture.LockExec(t, p.Ctx, editor, "BEGIN", 0)
						defer editor.ExecContext(p.Ctx, "ROLLBACK")
						fixture.LockExec(t, p.Ctx, editor, "ALTER TYPE tt_lock_pair ADD ATTRIBUTE note text CASCADE", 0)
						run(fixture.Exec("BEGIN", 0))
						defer owner.ExecContext(p.Ctx, "ROLLBACK")
						run(fixture.Exec("SAVEPOINT waiting", 0))
						ctx, cancel := context.WithCancel(p.Ctx)
						defer cancel()
						finished := make(chan struct{})
						join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
							defer close(finished)
							result, err := held.ExecContext(ctx)
							return fixture.RelationOutcome{Result: result, Err: err}
						})
						waitingRelation := backing
						if move {
							waitingRelation = 0
						}
						fixture.RelationLockPending(t, p, b, ownerPID, editorPID, waitingRelation, "AccessExclusiveLock", finished)
						if b.PG {
							var signaled bool
							if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", ownerPID).Scan(&signaled); err != nil || !signaled {
								t.Fatal("cancel composite identity", signaled, err)
							}
						} else {
							cancel()
						}
						outcome := join()
						if outcome.Result != nil || outcome.Rows != nil {
							t.Fatal("canceled identity returned output", outcome)
						}
						if b.PG {
							d := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
							if move {
								d.Context = fmt.Sprintf("while updating tuple %s in relation %q", sourceCTID, "pg_class")
							}
							fixture.AssertDiagnostic(t, outcome.Err, &d)
						} else {
							fixture.LexicalOrdinary(t, outcome.Err, context.Canceled)
						}
						run(fixture.Exec("ROLLBACK TO waiting", 0))
						run(fixture.Exec("ROLLBACK", 0))
						fixture.LockExec(t, p.Ctx, editor, "ROLLBACK", 0)
						// Reverse the roles: identity keeps source authority after Execute.
						run(fixture.Exec("BEGIN", 0))
						run(fixture.Exec("SAVEPOINT authority", 0))
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: owner, Statement: held, SQL: query}, b.Recorder, fixture.Exec(query, 0))
						finished = make(chan struct{})
						join = fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							defer close(finished)
							return fixture.RowExecute(ctx, editor, "ALTER TYPE tt_lock_pair ADD ATTRIBUTE fresh text CASCADE", false)
						})
						fixture.RelationLockPending(t, p, b, editorPID, ownerPID, waitingRelation, "AccessExclusiveLock", finished)
						if savepoint {
							run(fixture.Exec("ROLLBACK TO authority", 0))
							run(fixture.Exec("RELEASE authority", 0))
						} else {
							run(fixture.Exec("ROLLBACK", 0))
						}
						outcome = join()
						fixture.State(t, outcome.Err, "")
						if outcome.Result == nil {
							t.Fatal("released follower has no result")
						}
						if count, err := outcome.Result.RowsAffected(); err != nil || count != 0 {
							t.Fatal("follower count", count, err)
						}
						if savepoint {
							run(fixture.Exec("COMMIT", 0))
						}
						run(fixture.Select("SELECT id,fresh FROM tt_lock_typed", []string{"id", "fresh"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}}))
						run(fixture.Select("SELECT count(*) FROM pg_attribute WHERE attrelid='tt_lock_pair'::regclass AND attnum>0 AND NOT attisdropped", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
						run(fixture.Select("SELECT count(*) FROM pg_type WHERE typnamespace='tt_composite_dst'::regnamespace", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					})
				}
			})
		}
	}
}
