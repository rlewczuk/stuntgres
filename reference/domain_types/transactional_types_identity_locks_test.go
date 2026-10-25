package reference_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Identity updates contend on the source pg_type tuple, not on a fabricated
// scalar heap or every table that happens to consume the domain.
func TestReferenceTransactionalTypesIdentityLocks(t *testing.T) {
	for _, family := range []string{"DOMAIN", "TYPE"} {
		for _, move := range []bool{false, true} {
			for _, savepoint := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/move=%v/savepoint=%v", family, move, savepoint), func(t *testing.T) {
					fixture.SchemaReferenceCleanup(t, "tt_identity_dst")
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							owner, editor := fixture.LockConnections(t, p.Ctx, b.DB)
							run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, owner, b.Recorder, s) }
							for _, q := range []string{"CREATE SCHEMA tt_identity_dst", "CREATE DOMAIN tt_identity AS integer DEFAULT 1", "CREATE TABLE tt_identity_rows(v tt_identity)", "CREATE TABLE tt_identity_unrelated(v integer)"} {
								run(fixture.Exec(q, 0))
							}
							query := "ALTER " + family + " tt_identity RENAME TO tt_renamed"
							if move {
								query = "ALTER " + family + " tt_identity SET SCHEMA tt_identity_dst"
							}
							held, err := owner.PrepareContext(p.Ctx, query)
							if err != nil {
								t.Fatal(err)
							}
							defer held.Close()
							ownerPID, editorPID := 0, 0
							var sourceCTID string
							if b.PG {
								ownerPID, editorPID = fixture.RowPIDs(t, p.Ctx, owner, editor)
								if err := owner.QueryRowContext(p.Ctx, "SELECT ctid::text FROM pg_type WHERE oid='tt_identity'::regtype").Scan(&sourceCTID); err != nil {
									t.Fatal(err)
								}
							}
							fixture.LockExec(t, p.Ctx, editor, "BEGIN", 0)
							defer editor.ExecContext(p.Ctx, "ROLLBACK")
							fixture.LockExec(t, p.Ctx, editor, "ALTER DOMAIN tt_identity SET DEFAULT 7", 0)
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
							fixture.RelationLockPending(t, p, b, ownerPID, editorPID, 0, "", finished)
							fixture.LockExec(t, p.Ctx, b.DB, "INSERT INTO tt_identity_unrelated VALUES(42)", 1)
							if b.PG {
								var signaled bool
								if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", ownerPID).Scan(&signaled); err != nil || !signaled {
									t.Fatal("cancel identity", signaled, err)
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
								d.Context = fmt.Sprintf("while updating tuple %s in relation %q", sourceCTID, "pg_type")
								fixture.AssertDiagnostic(t, outcome.Err, &d)
							} else {
								fixture.LexicalOrdinary(t, outcome.Err, context.Canceled)
							}
							run(fixture.Exec("ROLLBACK TO waiting", 0))
							run(fixture.Exec("ROLLBACK", 0))
							fixture.LockExec(t, p.Ctx, editor, "ROLLBACK", 0)
							// Reuse the genuine pre-BEGIN handle. Its granted source authority
							// must survive Execute until transaction end or rollback to its mark.
							run(fixture.Exec("BEGIN", 0))
							run(fixture.Exec("SAVEPOINT authority", 0))
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: owner, Statement: held, SQL: query}, b.Recorder, fixture.Exec(query, 0))
							finished = make(chan struct{})
							join = fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
								defer close(finished)
								return fixture.RowExecute(ctx, editor, "ALTER DOMAIN tt_identity SET DEFAULT 9", false)
							})
							fixture.RelationLockPending(t, p, b, editorPID, ownerPID, 0, "", finished)
							if savepoint {
								run(fixture.Exec("ROLLBACK TO authority", 0))
								run(fixture.Exec("RELEASE authority", 0))
							} else {
								run(fixture.Exec("ROLLBACK", 0))
							}
							outcome = join()
							fixture.State(t, outcome.Err, "")
							if outcome.Result == nil {
								t.Fatal("released follower has no command result")
							}
							if affected, err := outcome.Result.RowsAffected(); err != nil || affected != 0 {
								t.Fatal("follower count", affected, err)
							}
							if savepoint {
								run(fixture.Exec("COMMIT", 0))
							}
							run(fixture.Exec("INSERT INTO tt_identity_rows DEFAULT VALUES", 1))
							run(fixture.Select("SELECT v FROM tt_identity_rows", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
							run(fixture.Select("SELECT v FROM tt_identity_unrelated", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
							run(fixture.Select("SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('tt_identity','_tt_identity')", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
							run(fixture.Select("SELECT count(*) FROM pg_type WHERE typnamespace='tt_identity_dst'::regnamespace", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
						})
					}
				})
			}
		}
	}
}
