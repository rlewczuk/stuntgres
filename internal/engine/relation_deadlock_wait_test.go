package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
	"testing"
	"testing/synctest"
	"time"
)

func TestDeadlockResultScheduling(t *testing.T) {
	t.Run("guard_resolution_completed", func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		// Exercise the selected guard branch directly: a randomized select
		// between two ready channels cannot guarantee a regression failure.
		if !deadlockGuardCompleted(done) {
			t.Fatal("failure guard rejected authoritative completed request")
		}
	})
	t.Run("guard_resolution_pending", func(t *testing.T) {
		if deadlockGuardCompleted(make(chan struct{})) {
			t.Fatal("failure guard accepted an unfinished request")
		}
	})
	t.Run("completion_first", func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		if !deadlockAwait(done, nil) {
			t.Fatal("already completed request rejected")
		}
		guard := make(chan time.Time, 1)
		guard <- time.Now()
		if !deadlockAwait(done, guard) {
			t.Fatal("simultaneously ready completion rejected")
		}
		select {
		case <-guard:
		default:
			t.Fatal("completion-first path consumed guard")
		}
	})
	t.Run("unfinished_at_guard", func(t *testing.T) {
		guard := make(chan time.Time, 1)
		guard <- time.Now()
		if deadlockAwait(make(chan struct{}), guard) {
			t.Fatal("unfinished request accepted")
		}
	})
	t.Run("delayed_completion", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			done, ready, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
			result := make(chan bool, 1)
			go func() {
				defer close(joined)
				guard := time.NewTimer(time.Second)
				defer guard.Stop()
				close(ready)
				result <- deadlockAwait(done, guard.C)
			}()
			<-ready
			synctest.Wait()
			select {
			case <-result:
				t.Error("wait returned before completion or guard")
			default:
			}
			close(done)
			<-joined
			select {
			case completed := <-result:
				if !completed {
					t.Error("released completion rejected")
				}
			default:
				t.Error("waiter returned no result")
			}
		})
	})
	t.Run("nil_request", func(t *testing.T) {
		if err := deadlockResult(t, nil); err != nil {
			t.Fatal(err)
		}
	})
	for _, outcome := range []string{"grant", "cancel", "timeout"} {
		t.Run("actual_request_"+outcome, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := New()
				a, ra := advisoryUnitOwner(t, e)
				b, rb := advisoryUnitOwner(t, e)
				key := lock.Resource{Database: e.catalog.Domain, Kind: lock.AdvisoryBigintResource, Low: 71}
				advisoryUnitGrant(t, a, ra, key, lock.AdvisoryExclusive)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				policy := lock.Policy{Source: ctx, Provenance: rb}
				if outcome == "timeout" {
					policy.Timeout = 70 * time.Millisecond
				}
				r, err := e.locks.StartAcquire(ctx, b.Advisory.Ticket, key, lock.AdvisoryExclusive, policy)
				if err != nil || r == nil {
					t.Fatal("actual pending request", r, err)
				}
				switch outcome {
				case "grant":
					advisoryUnitUnlock(t, a, key, lock.AdvisoryExclusive, true)
				case "cancel":
					cancel()
				case "timeout":
					time.Sleep(70 * time.Millisecond)
				}
				synctest.Wait()
				select {
				case <-r.Done():
				default:
					t.Fatal("request did not complete")
				}
				stored := r.Err()
				if got := deadlockResult(t, r); got != stored {
					t.Fatal("stored outcome identity changed", got, stored)
				}
				switch outcome {
				case "grant":
					if stored != nil {
						t.Fatal(stored)
					}
				case "cancel":
					var d *sqlstate.Error
					if !errors.Is(stored, context.Canceled) || errors.As(stored, &d) {
						t.Fatal("ordinary cancellation changed", stored)
					}
				case "timeout":
					var d *sqlstate.Error
					if !errors.As(stored, &d) || *d != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: "canceling statement due to lock timeout"}) {
						t.Fatal("timeout diagnostic changed", stored)
					}
				}
			})
		})
	}
}
