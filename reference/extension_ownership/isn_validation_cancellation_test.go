package reference_test

import (
	"context"
	"database/sql"
	"runtime"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIsnValidationAdmittedCancellation(t *testing.T) {
	isnValidationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn) {
		run := isnConversionRun(t, p, b, a)
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE iv_cancel_seed(id integer,v ean13)", 0))
		run(fixture.Exec("INSERT INTO iv_cancel_seed VALUES(1,'2222222222222'),(2,NULL)", 2))
		seed := fixture.Select("SELECT id,v::text AS v,is_valid(v) AS valid FROM iv_cancel_seed ORDER BY id", []string{"id", "v", "valid"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), "222-222222222-2", true}, {int64(2), nil, nil}})
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer isnValidationCleanup(t, peer)
		runPeer := isnConversionRun(t, p, b, peer)
		ownerPID, peerPID := fixture.LockPID(t, p.Ctx, a), fixture.LockPID(t, p.Ctx, peer)
		if ownerPID == peerPID {
			t.Fatal("cancellation owners share a physical backend")
		}
		const query = "SELECT pg_advisory_xact_lock(CASE WHEN public.isn_weak(true) THEN 76180421::bigint ELSE 76180422::bigint END)"
		held := fixture.IdentityHeld(t, p.Ctx, a, query)
		defer held.Statement.Close()
		locked := false
		defer func() {
			if locked {
				if _, err := peer.ExecContext(p.Ctx, "ROLLBACK"); err != nil {
					t.Error("cancellation blocker cleanup", err)
				}
			}
		}()
		for _, prepared := range []bool{false, true} {
			isnValidationMode(run, false)
			isnValidationMode(runPeer, false)
			run(seed)
			runPeer(fixture.Exec("BEGIN", 0))
			runPeer(fixture.Select("SELECT pg_advisory_xact_lock(76180421::bigint) AS held", []string{"held"}, []string{"VOID"}, [][]any{{""}}))
			locked = true
			// The alternate key is not held. Reaching the exact requested key
			// therefore proves the dependent setter returned true before waiting;
			// no target-list order, elapsed time or prewalk override is involved.
			fixture.BinaryConversionActiveCancelAfter(t, p, b, a, query, "(*relationRequest).wait", func(ctx context.Context) {
				queued := "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND database=(SELECT oid FROM pg_database WHERE datname=current_database()) AND classid::bigint=0 AND objid::bigint=76180421 AND objsubid=1 AND mode='ExclusiveLock' AND NOT granted) AND $2=ANY(pg_blocking_pids($1)) AS v"
				for {
					var waiting bool
					if err := peer.QueryRowContext(ctx, queued, ownerPID, peerPID).Scan(&waiting); err != nil {
						t.Fatal("post-setter advisory admission", err)
					}
					if waiting {
						break
					}
					if err := ctx.Err(); err != nil {
						t.Fatal("post-setter advisory admission deadline", err)
					}
					runtime.Gosched()
				}
				fixture.RunStep(t, ctx, peer, b.Recorder, isnValidationBool(queued, true, ownerPID, peerPID))
				fixture.RunStep(t, ctx, peer, b.Recorder, isnValidationBool("SELECT isn_weak() AS v", false))
				fixture.RunStep(t, ctx, peer, b.Recorder, seed)
			}, func(ctx context.Context) fixture.RelationOutcome {
				var result sql.Result
				var err error
				if prepared {
					result, err = held.Statement.ExecContext(ctx)
				} else {
					result, err = a.ExecContext(ctx, query)
				}
				return fixture.RelationOutcome{Result: result, Err: err}
			})
			// The fixture joins cancellation before this same pinned owner is used.
			// PostgreSQL is canceled server-side, never by retiring its connection.
			if fixture.LockPID(t, p.Ctx, a) != ownerPID {
				t.Fatal("cancellation replaced the physical owner")
			}
			isnValidationSetting(run, false)
			run(isnValidationBool("SELECT isn_weak() AS v", false))
			bad := isnValidationQueryError("SELECT is_valid($1::text::ean13) AS v", isnValidationChecksum(0, isnValidationBad[0]), isnValidationBad[0])
			if prepared {
				bad = fixture.Prepared(bad)
			}
			run(bad)
			run(isnValidationBool("SELECT is_valid($1::text::ean13) AS v", true, "2222222222222"))
			run(seed)
			isnValidationSetting(runPeer, false)
			runPeer(isnValidationBool("SELECT isn_weak() AS v", false))
			runPeer(seed)
			runPeer(fixture.Exec("ROLLBACK", 0))
			locked = false
			run(isnValidationBool("SELECT pg_try_advisory_xact_lock(76180421::bigint) AS v", true))
			runPeer(isnValidationBool("SELECT pg_try_advisory_xact_lock(76180421::bigint) AS v", true))
		}
	})
}
