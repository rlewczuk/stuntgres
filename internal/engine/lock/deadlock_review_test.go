package lock

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
				f := newLockFixture(t)
				at, bt := f.tx(), f.tx()
				x, y := f.relation(), f.relation()
				f.request(at, x, AccessExclusive)
				f.request(bt, y, AccessExclusive)
				first := f.request(at, y, AccessExclusive)

				realContext, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				gate := &deadlockNormalizationContext{Context: realContext, observed: make(chan struct{}), resume: make(chan struct{})}
				finished := make(chan struct{})
				var closer *relationRequest
				var interrupted, survivor *relationRequest
				go func() {
					var notify *relationRequest
					f.m.mu.Lock()
					// Journal the closing request under the existing manager contract so
					// the barrier targets detector normalization, not StartAcquire's
					// earlier admission checks or its initial settle.
					entry := f.m.entries[x]
					closer = &relationRequest{ticket: bt, provenance: bt.Ref(), ctx: context.Background(), mode: AccessExclusive, done: make(chan struct{}), ref: x, entry: entry}
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
						interrupted.policy.Source = realContext
					}
					gate.armed = true
					f.m.detectDeadlock(closer, &notify)
					f.m.mu.Unlock()
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
				f.m.mu.Lock()
				interruptedTerminal, interruption := interrupted.terminal, interrupted.Err()
				survivorTerminal, survivorError := survivor.terminal, survivor.Err()
				holdsRetained := f.m.entries[x].holders[at].Mode&AccessExclusive != 0 && f.m.entries[y].holders[bt].Mode&AccessExclusive != 0
				f.m.mu.Unlock()
				if !interruptedTerminal || !errors.Is(interruption, context.Canceled) {
					t.Errorf("interrupted request must retain canonical cancellation, terminal=%v error=%v", interruptedTerminal, interruption)
				}
				var diagnostic *sqlstate.Error
				if errors.As(interruption, &diagnostic) {
					t.Errorf("caller cancellation fabricated SQLSTATE %s", diagnostic.Code)
				}
				if source && interruptedTerminal && errors.Is(interruption, context.Canceled) {
					var original SourceError
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
				f.m.End(interrupted.ticket)
				if err := f.result(survivor); err != nil {
					t.Errorf("survivor after interrupted owner's end: %v", err)
				}
				f.m.End(survivor.ticket)
				f.empty()
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
		f := newLockFixture(t)
		ht := f.tx()
		resource := f.relation()
		f.request(ht, resource, AccessExclusive)
		tickets := make([]*Ticket, 0, count)
		requests := make([]*relationRequest, 0, count)
		for range count {
			ticket := f.tx()
			tickets = append(tickets, ticket)
			// Use the actual supported enqueue path. Background contexts avoid
			// timer/callback allocations unrelated to the sparse graph itself.
			r, err := f.m.StartAcquire(context.Background(), ticket, resource, AccessShare, Policy{})
			if err != nil || r == nil {
				t.Fatalf("AS waiter %d: request=%v error=%v", len(requests), r, err)
			}
			requests = append(requests, r)
		}
		f.m.mu.Lock()
		entry := f.m.entries[resource]
		if len(entry.pending) != count || len(entry.holders) != 1 || entry.holders[ht].Mode != AccessExclusive {
			f.m.mu.Unlock()
			t.Fatal("measurement requires exactly one AX holder and all AS readers pending")
		}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for range detections {
			var notify *relationRequest
			f.m.detectDeadlock(requests[len(requests)-1], &notify)
			if notify != nil {
				f.m.mu.Unlock()
				t.Fatal("acyclic compatible queue produced a terminal decision")
			}
		}
		runtime.ReadMemStats(&after)
		for _, r := range requests {
			if r.terminal || r.mode != AccessShare {
				f.m.mu.Unlock()
				t.Fatal("compatible reader queue changed during measurement")
			}
		}
		f.m.mu.Unlock()
		bytes := (after.TotalAlloc - before.TotalAlloc) / detections
		if previous != 0 && bytes >= 8*previous {
			t.Errorf("sparse AX/AS detector allocation grew quadratically: %d waiters use %d bytes/detection; %d waiters used %d (require growth <8x for 4x waiters)", count, bytes, count/4, previous)
		}
		previous = bytes
		f.m.End(ht)
		for _, r := range requests {
			if err := f.result(r); err != nil {
				t.Fatal("compatible reader failed after AX release", err)
			}
			f.m.mu.Lock()
			granted := f.m.entries[resource].holders[r.ticket].Mode&AccessShare != 0
			f.m.mu.Unlock()
			if !granted {
				t.Fatal("reader was not granted after AX release")
			}
		}
		for _, ticket := range tickets {
			f.m.End(ticket)
		}
		f.empty()
	}
}
