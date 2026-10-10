package engine

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/sqlstate"
)

// Err samples the real context before the barrier. Cancellation can then become
// effective while the detector owns metadata, but its initial normalization sees
// the already-sampled nil. Subsequent checks delegate to the real context. This
// models cancellation racing an error observation, not a second error policy.
type deadlockNormalizationContext struct {
	context.Context
	observed chan struct{}
	resume   chan struct{}
	armed    bool // armed and consumed only while manager metadata is held
}

func (c *deadlockNormalizationContext) Err() error {
	err := c.Context.Err()
	if c.armed {
		c.armed = false
		close(c.observed)
		<-c.resume
	}
	return err
}

func TestRelationDeadlockInterruptionDuringDetection(t *testing.T) {
	for _, source := range []bool{false, true} {
		for _, interruptCloser := range []bool{false, true} {
			name := map[bool]string{false: "caller", true: "source"}[source] + "/" + map[bool]string{false: "predecessor", true: "closer"}[interruptCloser]
			t.Run(name, func(t *testing.T) {
				e := New()
				a := sessionStart(t, e, nil)
				b := sessionStart(t, e, nil)
				sessionSQL(t, a, "CREATE TABLE x(v INT)")
				sessionSQL(t, a, "CREATE TABLE y(v INT)")
				transactionBegin(t, a)
				transactionBegin(t, b)
				at, bt := a.transactions.ticket, b.transactions.ticket
				t.Cleanup(func() { e.locks.end(at); e.locks.end(bt) })
				x := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "x")}
				y := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "y")}
				deadlockRequest(t, e, at, x, accessExclusive)
				deadlockRequest(t, e, bt, y, accessExclusive)
				first := deadlockRequest(t, e, at, y, accessExclusive)

				realContext, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				gate := &deadlockNormalizationContext{Context: realContext, observed: make(chan struct{}), resume: make(chan struct{})}
				finished := make(chan struct{})
				var closer *relationRequest
				var interrupted, survivor *relationRequest
				go func() {
					var notify *relationRequest
					e.locks.mu.Lock()
					// Journal the closing request under the existing manager contract so
					// the barrier targets detector normalization, not startAcquire's
					// earlier admission checks or its initial settle.
					entry := e.locks.entries[x]
					closer = &relationRequest{ticket: bt, provenance: bt.ref, ctx: context.Background(), mode: accessExclusive, done: make(chan struct{}), ref: x, entry: entry}
					bt.resources[x] = struct{}{}
					entry.pending = append(entry.pending, closer)
					interrupted, survivor = first, closer
					if interruptCloser {
						interrupted, survivor = closer, first
					}
					interrupted.ctx = gate
					if source {
						// Gate the ordinary ctx.Err observation, after the source
						// cancellation check has sampled the still-live source.
						gate.Context = context.Background()
						interrupted.policy.source = realContext
					}
					gate.armed = true
					e.locks.detectDeadlock(closer, &notify)
					e.locks.mu.Unlock()
					notifyRequests(notify)
					close(finished)
				}()
				// Both barriers have failure guards only; neither time nor scheduling
				// establishes queue membership or the cancellation observation.
				select {
				case <-gate.observed:
				case <-time.After(3 * time.Second):
					close(gate.resume)
					<-finished
					t.Fatal("detector did not reach initial normalization")
				}
				cancel()
				if realContext.Err() != context.Canceled {
					t.Fatal("real cancellation was not effective at detector barrier")
				}
				close(gate.resume)
				select {
				case <-finished:
				case <-time.After(3 * time.Second):
					t.Fatal("detector did not finish")
				}
				e.locks.mu.Lock()
				interruptedTerminal, interruption := interrupted.terminal, interrupted.err
				survivorTerminal, survivorError := survivor.terminal, survivor.err
				holdsRetained := e.locks.entries[x].holders[at].mode&accessExclusive != 0 && e.locks.entries[y].holders[bt].mode&accessExclusive != 0
				e.locks.mu.Unlock()
				if !interruptedTerminal || !errors.Is(interruption, context.Canceled) {
					t.Errorf("interrupted request must retain canonical cancellation, terminal=%v error=%v", interruptedTerminal, interruption)
				}
				var diagnostic *sqlstate.Error
				if errors.As(interruption, &diagnostic) {
					t.Errorf("caller cancellation fabricated SQLSTATE %s", diagnostic.Code)
				}
				if source && interruptedTerminal && errors.Is(interruption, context.Canceled) {
					var original relationSourceError
					if !errors.As(interruption, &original) || original.error != context.Canceled {
						t.Errorf("source cancellation lost canonical identity: %v", interruption)
					}
				} else if !source && interruption != context.Canceled {
					t.Errorf("caller cancellation identity changed: %v", interruption)
				}
				if survivorTerminal {
					t.Errorf("interrupted cycle caused collateral terminal decision: %v", survivorError)
				}
				if !holdsRetained {
					t.Error("detector released transaction holds instead of leaving cleanup to owner")
				}
				// Model Finish/end only after inspecting the detector decision. The
				// other owner must then grant normally, never receive a delayed 40P01.
				e.locks.end(interrupted.ticket)
				if err := deadlockResult(t, survivor); err != nil {
					t.Errorf("survivor after interrupted owner's end: %v", err)
				}
				e.locks.end(survivor.ticket)
				lockEmpty(t, e)
			})
		}
	}
}

func TestRelationDeadlockCompatibleWaiterAllocationScaling(t *testing.T) {
	// Fourfold growth of a sparse graph must not approach the sixteenfold
	// allocation growth of reserving all hypothetical queue-order pairs. Measure
	// bytes, not allocation count: one oversized allocation is still the bug.
	const detections = 5
	var previous uint64
	for _, count := range []int{32, 128, 512} {
		e := New()
		holder := sessionStart(t, e, nil)
		sessionSQL(t, holder, "CREATE TABLE readers(v INT)")
		transactionBegin(t, holder)
		ht := holder.transactions.ticket
		resource := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "readers")}
		deadlockRequest(t, e, ht, resource, accessExclusive)
		tickets := make([]*relationTicket, 0, count)
		requests := make([]*relationRequest, 0, count)
		for range count {
			s := sessionStart(t, e, nil)
			transactionBegin(t, s)
			ticket := s.transactions.ticket
			tickets = append(tickets, ticket)
			// Use the actual supported enqueue path. Background contexts avoid
			// timer/callback allocations unrelated to the sparse graph itself.
			r, err := e.locks.startAcquire(context.Background(), ticket, resource, accessShare, relationLockPolicy{})
			if err != nil || r == nil {
				t.Fatalf("AS waiter %d: request=%v error=%v", len(requests), r, err)
			}
			requests = append(requests, r)
		}
		t.Cleanup(func() {
			e.locks.end(ht)
			for _, ticket := range tickets {
				e.locks.end(ticket)
			}
		})
		e.locks.mu.Lock()
		entry := e.locks.entries[resource]
		if len(entry.pending) != count || len(entry.holders) != 1 || entry.holders[ht].mode != accessExclusive {
			e.locks.mu.Unlock()
			t.Fatal("measurement requires exactly one AX holder and all AS readers pending")
		}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for range detections {
			var notify *relationRequest
			e.locks.detectDeadlock(requests[len(requests)-1], &notify)
			if notify != nil {
				e.locks.mu.Unlock()
				t.Fatal("acyclic compatible queue produced a terminal decision")
			}
		}
		runtime.ReadMemStats(&after)
		for _, r := range requests {
			if r.terminal || r.mode != accessShare {
				e.locks.mu.Unlock()
				t.Fatal("compatible reader queue changed during measurement")
			}
		}
		e.locks.mu.Unlock()
		bytes := (after.TotalAlloc - before.TotalAlloc) / detections
		if previous != 0 && bytes >= 8*previous {
			t.Errorf("sparse AX/AS detector allocation grew quadratically: %d waiters use %d bytes/detection; %d waiters used %d (require growth <8x for 4x waiters)", count, bytes, count/4, previous)
		}
		previous = bytes
		e.locks.end(ht)
		for _, r := range requests {
			if err := deadlockResult(t, r); err != nil {
				t.Fatal("compatible reader failed after AX release", err)
			}
			e.locks.mu.Lock()
			granted := e.locks.entries[resource].holders[r.ticket].mode&accessShare != 0
			e.locks.mu.Unlock()
			if !granted {
				t.Fatal("reader was not granted after AX release")
			}
		}
		for _, ticket := range tickets {
			e.locks.end(ticket)
		}
		lockEmpty(t, e)
	}
}
