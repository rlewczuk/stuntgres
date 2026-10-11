package reference_test

import (
	"bytes"
	"context"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDriverBatchRefreshCancellation(t *testing.T) {
	batchReviewControlsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
		batchRefreshSeed(t, p, b, f.A)
		batchExec(t, p, b, f.A, "CREATE MATERIALIZED VIEW batch_refresh_blocked AS SELECT i,s FROM batch_refresh_base", 3)
		observer, err := f.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer observer.Close()
		holder := fixture.QueryLockBegin(t, p.Ctx, f.B)
		defer holder.Rollback()
		batchRefreshRows(t, p, b, holder, "batch_refresh_blocked", batchRefreshOld())
		ctx, cancel := context.WithTimeout(p.Ctx, 2*time.Second)
		defer cancel()
		pid, blocker := 0, 0
		var oid int64
		if b.PG {
			pid = fixture.LockPID(t, p.Ctx, f.A)
			blocker = fixture.LockPID(t, p.Ctx, f.B)
			oid = fixture.LockOID(t, p.Ctx, "batch_refresh_blocked")
		}
		q := "INSERT INTO batch_refresh_base VALUES(3,'c'); REFRESH MATERIALIZED VIEW batch_refresh_mv; REFRESH MATERIALIZED VIEW batch_refresh_blocked; INSERT INTO batch_refresh_base VALUES(99,'skipped')"
		done := make(chan error, 1)
		go func() {
			r, err := f.A.ExecContext(ctx, q)
			if r != nil {
				err = errors.New("canceled refresh message returned a result")
			}
			done <- err
		}()
		joined := false
		defer func() {
			cancel()
			holder.Rollback()
			if !joined {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("refresh cancellation worker failed to join")
				}
			}
		}()
		if b.PG {
			// Observe the actual second refresh wait, not a callback or started signal.
			fixture.LockWaitBehind(t, p.Ctx, pid, blocker, oid, "AccessExclusiveLock")
			cancel()
		}
		select {
		case err := <-done:
			joined = true
			if b.PG {
				w := batchDiag("57014", "canceling statement due to user request")
				fixture.AssertDiagnostic(t, err, &w)
			} else if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("native retained refresh cancellation: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("refresh cancellation did not finish")
		}
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"batch_refresh_base", "batch_refresh_mv", "batch_refresh_blocked"} {
			batchRefreshRows(t, p, b, observer, name, batchRefreshOld())
		}
		batchRefreshPopulation(t, p, b, observer, "batch_refresh_mv", true)
		batchRefreshPopulation(t, p, b, observer, "batch_refresh_blocked", true)
		if !b.PG {
			// Implicit cancellation leaves this physical owner usable and no prefix published.
			batchRefreshRows(t, p, b, f.A, "batch_refresh_mv", batchRefreshOld())
		}
		batchExec(t, p, b, observer, "INSERT INTO batch_refresh_base VALUES(4,'recovery'); REFRESH MATERIALIZED VIEW batch_refresh_mv; REFRESH MATERIALIZED VIEW batch_refresh_blocked", 0)
		four := append(batchRefreshOld(), []any{int64(4), "recovery"})
		batchRefreshRows(t, p, b, observer, "batch_refresh_mv", four)
		batchRefreshRows(t, p, b, observer, "batch_refresh_blocked", four)
	})
}

func TestReferenceDriverBatchRefreshControls(t *testing.T) {
	batchReviewControlsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
		batchRefreshSeed(t, p, b, f.A)
		four := append(batchRefreshOld(), []any{int64(3), "c"})
		q := "SELECT i,s FROM batch_refresh_mv; INSERT INTO batch_refresh_base VALUES(3,'c'); REFRESH MATERIALIZED VIEW batch_refresh_mv; SELECT i,s FROM batch_refresh_mv"
		r := batchQuery(t, p, f.A, q)
		if !b.PG {
			// Real control APIs replace the engine epoch; completed output still owns
			// its old and private replacement values, not live storage pointers.
			var saved bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
			batchRefreshRows(t, p, b, f.B, "batch_refresh_mv", four)
			batchRefreshPopulation(t, p, b, f.B, "batch_refresh_mv", true)
			batchExec(t, p, b, f.B, "REFRESH MATERIALIZED VIEW batch_refresh_mv", 0)
		} else {
			// PostgreSQL has no Reset/Restore API; this independently checks retained
			// completed message outputs against a later committed replacement.
			batchExec(t, p, b, f.B, "INSERT INTO batch_refresh_base VALUES(4,'later'); REFRESH MATERIALIZED VIEW batch_refresh_mv", 0)
		}
		batchSets(t, r, batchRefreshSet(batchRefreshOld()...), batchRefreshSet(four...))
		want := four
		if b.PG {
			want = append(append(batchRefreshOld(), []any{int64(3), "c"}), []any{int64(4), "later"})
		}
		batchRefreshRows(t, p, b, f.B, "batch_refresh_mv", want)
		batchRefreshPopulation(t, p, b, f.B, "batch_refresh_mv", true)
	})
}
