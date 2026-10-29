package reference_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// The publication already contains publication_rt_t. ADD must reach the real
// candidate table's lock before cancellation, and may never publish its mapping.
func publicationAdmittedCancellation(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	publicationRun(t, p, b, b.DB, fixture.Exec("CREATE TABLE publication_rt_candidate(id integer)", 0))
	for _, prepared := range []bool{false, true} {
		func() {
			holder := publicationConn(t, p, b)
			defer holder.Close()
			waiter := publicationConn(t, p, b)
			defer waiter.Close()
			q := "ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_candidate"
			var execute func(context.Context) fixture.RelationOutcome
			if prepared {
				stmt, err := waiter.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				execute = func(ctx context.Context) fixture.RelationOutcome {
					result, err := stmt.ExecContext(ctx)
					return fixture.RelationOutcome{Result: result, Err: err}
				}
			} else {
				execute = func(ctx context.Context) fixture.RelationOutcome {
					result, err := waiter.ExecContext(ctx, q)
					return fixture.RelationOutcome{Result: result, Err: err}
				}
			}
			waiterPID := fixture.LockPID(t, p.Ctx, waiter)
			holderPID := fixture.LockPID(t, p.Ctx, holder)
			var candidateOID int64
			if !b.PG {
				if err := waiter.QueryRowContext(p.Ctx, "SELECT 'publication_rt_candidate'::regclass::oid::bigint").Scan(&candidateOID); err != nil || candidateOID <= 0 {
					t.Fatal("actual native candidate relation identity", candidateOID, err)
				}
			}
			publicationRun(t, p, b, holder, fixture.Exec("BEGIN", 0), fixture.Exec("LOCK TABLE publication_rt_candidate IN ACCESS EXCLUSIVE MODE", 0))
			defer holder.ExecContext(p.Ctx, "ROLLBACK")
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			completed := make(chan struct{})
			join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
				defer close(completed)
				return execute(ctx)
			})
			joined := false
			defer func() {
				cancel()
				if !joined {
					join()
				}
			}()
			if b.PG {
				fixture.LockWaitBehind(t, p.Ctx, waiterPID, holderPID, fixture.LockOID(t, p.Ctx, "publication_rt_candidate"), "ShareUpdateExclusiveLock")
				var canceled bool
				if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", waiterPID).Scan(&canceled); err != nil || !canceled {
					t.Fatal("cancel admitted publication command", canceled, err)
				}
			} else {
				publicationNativeLockAdmission(t, p, b, waiterPID, holderPID, candidateOID, completed)
				cancel()
			}
			outcome := join()
			joined = true
			if outcome.Result != nil {
				t.Fatal("canceled publication command exposed result", outcome.Result)
			}
			if b.PG {
				want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
				fixture.AssertDiagnostic(t, outcome.Err, &want)
			} else if !errors.Is(outcome.Err, context.Canceled) {
				t.Fatal("admitted native publication cancellation", outcome.Err)
			}
			publicationRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
		}()
		publicationRun(t, p, b, b.DB, publicationExplicit("publication_rt_p", []any{"public", "publication_rt_t"}), publicationMembers("publication_rt_p", []any{"public", "publication_rt_t"}), publicationOptions("publication_rt_p", false, true, false, false, false, false, "n"))
	}
}

// Observe the real public lock rows, not a scheduler stack-name convention:
// publication utility admission uses a catalog-resource relation request.
func publicationNativeLockAdmission(t *testing.T, p *fixture.Pair, b fixture.Backend, waiter, holder int, relation int64, completed <-chan struct{}) {
	t.Helper()
	if waiter <= 0 || holder <= 0 || waiter == holder || relation <= 0 {
		t.Fatal("invalid native publication blocker identities", waiter, holder, relation)
	}
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	requireRunning := func() {
		select {
		case <-completed:
			t.Fatal("publication ADD completed before observed lock admission")
		default:
		}
	}
	for {
		requireRunning()
		var admitted bool
		err := b.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE pid=$1 AND locktype='relation' AND relation=$3 AND mode='ShareUpdateExclusiveLock' AND NOT granted) AND EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE pid=$2 AND locktype='relation' AND relation=$3 AND mode='AccessExclusiveLock' AND granted)`, waiter, holder, relation).Scan(&admitted)
		if err != nil {
			t.Fatal("observing native publication relation admission", err)
		}
		if admitted {
			requireRunning()
			return
		}
		if err := ctx.Err(); err != nil {
			t.Fatal("native publication relation admission not observed", err)
		}
		runtime.Gosched()
	}
}
