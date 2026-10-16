package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// A consumer behind a retained DDL lock cannot read even the committed old
// definition. Observe that wait, end the candidate segment, then require the
// committed new value. Do not turn a blocking SELECT into an invisibility probe.
func batchUtilityBlockedPeer(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn, q, typ string, want any) {
	t.Helper()
	if !b.PG {
		fixture.ViewNativeBlocked(t, p.Ctx, peer, q, false)
		batchExec(t, p, b, a, "COMMIT", 0)
		fixture.BatchSets(t, batchQuery(t, p, peer, q), fixture.BatchSet{[]string{"v"}, []string{typ}, [][]any{{want}}, false})
		return
	}
	pid := fixture.LockPID(t, p.Ctx, peer)
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	type outcome struct {
		rows *sql.Rows
		err  error
	}
	done := make(chan outcome, 1)
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			select {
			case o := <-done:
				if o.rows != nil {
					o.rows.Close()
				}
			case <-time.After(5 * time.Second):
				t.Error("utility peer did not join")
			}
		}
	})
	go func() { r, err := peer.QueryContext(ctx, q); done <- outcome{r, err} }()
	fixture.LockWait(t, ctx, pid)
	batchExec(t, p, b, a, "COMMIT", 0)
	o := <-done
	joined = true
	if o.err != nil {
		if o.rows != nil {
			o.rows.Close()
		}
		t.Fatal("queued utility consumer", o.err)
	}
	fixture.BatchSets(t, o.rows, fixture.BatchSet{[]string{"v"}, []string{typ}, [][]any{{want}}, false})
}
