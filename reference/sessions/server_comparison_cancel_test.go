//go:build !noservertests

package reference_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// This is a comparison-bearing held statement, not a timed pg_sleep probe.
// A real AccessShareLock waiter is observed before the single CancelRequest.
func TestReferenceServerComparisonCancellation(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			owner := serverAuthenticated(t, f.DSNFor(b))
			serverWantCode(t, owner.query(t, "CREATE TABLE comparison_cancel(v name); INSERT INTO comparison_cancel VALUES ('public')"), "")
			victim := serverAuthenticated(t, f.DSNFor(b))
			serverComparisonDescription(t, serverCycle(t, victim, serverParse("cancel_compare", "SELECT v=$1 FROM comparison_cancel", 1043), serverDescribe('S', "cancel_compare")), []uint32{1043}, []uint32{16})
			serverWantCode(t, owner.query(t, "BEGIN; LOCK TABLE comparison_cancel IN ACCESS EXCLUSIVE MODE"), "")
			defer func() { serverWantCode(t, owner.query(t, "ROLLBACK"), "") }()
			victim.send(t, serverBind("", "cancel_compare", nil, [][]byte{[]byte("public")}))
			victim.send(t, serverExecute("", 0))
			victim.send(t, serverPacket('S', nil))
			observer := f.RawPool(t, b)
			ctx, cancel := context.WithTimeout(f.Ctx, 10*time.Second)
			defer cancel()
			for {
				var waiting bool
				var err error
				if b.PG {
					err = observer.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='relation' AND relation='comparison_cancel'::regclass::oid AND mode='AccessShareLock' AND NOT granted)`, int(victim.pid)).Scan(&waiting)
				} else {
					err = observer.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype='relation' AND relation='comparison_cancel'::regclass::oid AND mode='AccessShareLock' AND NOT granted)`).Scan(&waiting)
				}
				if err != nil {
					t.Fatal("comparison cancellation lock barrier:", err)
				}
				if waiting {
					break
				}
				runtime.Gosched()
			}
			type reply struct {
				frames []serverFrame
				err    error
			}
			done := make(chan reply, 1)
			go func() {
				var frames []serverFrame
				for {
					frame, err := victim.readFrame()
					if err != nil {
						done <- reply{frames, err}
						return
					}
					frames = append(frames, frame)
					if frame.tag == 'Z' {
						done <- reply{frames, nil}
						return
					}
				}
			}()
			joined := false
			defer func() {
				if !joined {
					victim.Close()
					<-done
				}
			}()
			serverCancel(t, f.DSNFor(b), victim.pid, victim.key)
			select {
			case r := <-done:
				joined = true
				if r.err != nil {
					t.Fatal(r.err)
				}
				serverComparisonError(t, r.frames, "57014", "canceling statement due to user request", "", "")
				serverWantReady(t, r.frames, 'I')
			case <-ctx.Done():
				victim.Close()
				serverWantCode(t, owner.query(t, "ROLLBACK"), "")
				t.Fatal("comparison cancel worker did not join")
			}
			serverWantCode(t, owner.query(t, "ROLLBACK"), "")
			serverWantRaw(t, serverCycle(t, victim, serverBind("", "cancel_compare", nil, [][]byte{[]byte("public")}), serverExecute("", 0)), [][][]byte{{[]byte("t")}})
			serverWantRaw(t, serverCycle(t, victim, serverBind("", "cancel_compare", nil, [][]byte{nil}), serverExecute("", 0)), [][][]byte{{nil}})
			serverComparisonDescription(t, serverCycle(t, victim, serverDescribe('S', "cancel_compare")), []uint32{1043}, []uint32{16})
		})
	}
}
