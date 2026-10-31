package core_test

import (
	"context"
	"testing"
	"time"
)

// The channel closes only after the worker's operation defers have completed.
// Both normal assertions and failure cleanup consume the same owned outcome.
func deleteUsingJoin(t *testing.T, cancel context.CancelFunc, done <-chan deleteOutcome) func() deleteOutcome {
	t.Helper()
	var got deleteOutcome
	received, joined := false, false
	join := func(fatal bool) {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case next, ok := <-done:
				if !ok {
					joined = true
					if !received {
						t.Error("DELETE USING worker exited without an outcome")
					}
					return
				}
				if received {
					if next.result.Rows != nil {
						next.result.Rows.Close()
					}
					t.Error("DELETE USING worker produced more than one outcome")
					continue
				}
				got, received = next, true
			case <-timer.C:
				if fatal {
					t.Fatal("DELETE USING worker did not join")
				}
				t.Error("DELETE USING worker cleanup did not join")
				return
			}
		}
	}
	t.Cleanup(func() {
		cancel()
		if !joined {
			join(false)
		}
		if got.result.Rows != nil {
			got.result.Rows.Close()
		}
	})
	return func() deleteOutcome {
		t.Helper()
		if !joined {
			join(true)
		}
		return got
	}
}
