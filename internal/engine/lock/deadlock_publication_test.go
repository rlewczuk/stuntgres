package lock

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/sqlstate"
)

// All observation state is accessed by the detector while metadata is held.
// Channels transfer ownership of policy/context changes at the barrier.
type deadlockObservationContext struct {
	context.Context
	observe func()
}

func (c *deadlockObservationContext) Err() error {
	err := c.Context.Err()
	c.observe()
	return err
}

// The deadline is advanced only while the detector is paused at a barrier.
// No timer callback or wall-clock delay is required to establish expiration.
type deadlockDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c *deadlockDeadlineContext) Deadline() (time.Time, bool) {
	return c.deadline, !c.deadline.IsZero()
}

func deadlockPublicationRun(t *testing.T, m *Manager, request *relationRequest, observed, resume chan struct{}, advance func()) {
	t.Helper()
	finished := make(chan struct{})
	go func() {
		var notify *relationRequest
		m.mu.Lock()
		m.detectDeadlock(request, &notify)
		m.mu.Unlock()
		notifyRequests(notify)
		close(finished)
	}()
	select {
	case <-observed:
		advance()
		close(resume)
	case <-time.After(3 * time.Second):
		close(resume)
		<-finished
		t.Fatal("detector did not reach observation barrier")
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("detector did not finish after observation barrier")
	}
}

// Journal a private request without running detection, as in the existing review
// regression. This isolates detector observations from StartAcquire/settle.
func deadlockPublicationPending(m *Manager, ticket *Ticket, ref Resource, mode Mode, ctx context.Context) *relationRequest {
	entry := m.entries[ref]
	r := &relationRequest{ticket: ticket, provenance: ticket.Ref(), ctx: ctx, mode: mode, done: make(chan struct{}), ref: ref, entry: entry}
	ticket.resources[ref] = struct{}{}
	entry.pending = append(entry.pending, r)
	return r
}

func TestRelationDeadlockFinalSweepInterruption(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller", true: "source"}[source], func(t *testing.T) {
			f := newLockFixture(t)
			at, bt := f.tx(), f.tx()
			ref := f.relation()
			f.request(at, ref, AccessShare)
			f.request(bt, ref, AccessShare)
			real, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			observed, resume := make(chan struct{}), make(chan struct{})
			var trace []string
			victimChecks, laterChecks := 0, 0
			victimContext := &deadlockObservationContext{Context: real, observe: func() {
				victimChecks++
				if victimChecks == 2 {
					trace = append(trace, "victim-final")
				}
			}}
			laterContext := &deadlockObservationContext{Context: context.Background(), observe: func() {
				laterChecks++
				if laterChecks == 2 {
					trace = append(trace, "later-final")
					close(observed)
					<-resume
				}
			}}
			f.m.mu.Lock()
			// One authoritative queue guarantees observation order, independently
			// of map iteration: both AS holders request AX conversions. Each AX
			// has a HARD dependency on the other owner's retained AS hold.
			victim := deadlockPublicationPending(f.m, at, ref, AccessExclusive, victimContext)
			later := deadlockPublicationPending(f.m, bt, ref, AccessExclusive, laterContext)
			if source {
				victimContext.Context = context.Background()
				victim.policy.Source = real
			}
			f.m.mu.Unlock()
			deadlockPublicationRun(t, f.m, victim, observed, resume, func() {
				cancel()
				if real.Err() != context.Canceled {
					t.Error("cancellation not effective during later final observation")
				}
			})
			if len(trace) != 2 || trace[0] != "victim-final" || trace[1] != "later-final" {
				t.Fatalf("wrong explicit final-sweep observation order: %v", trace)
			}
			err := f.result(victim)
			if !errors.Is(err, context.Canceled) {
				t.Errorf("victim must retain cancellation, got %v", err)
			}
			var diagnostic *sqlstate.Error
			if errors.As(err, &diagnostic) {
				t.Errorf("interruption fabricated SQLSTATE %s", diagnostic.Code)
			}
			if source {
				var original SourceError
				if !errors.As(err, &original) || original.error != context.Canceled {
					t.Errorf("source identity lost: %v", err)
				}
			} else if err != context.Canceled {
				t.Errorf("caller error identity lost: %v", err)
			}
			f.m.mu.Lock()
			collateral := later.terminal
			holds := f.m.entries[ref].holders[at].Mode == AccessShare && f.m.entries[ref].holders[bt].Mode == AccessShare
			f.m.mu.Unlock()
			if collateral || !holds {
				t.Errorf("collateral terminal=%v, original holds retained=%v", collateral, holds)
			}
			f.m.End(at)
			if err := f.result(later); err != nil {
				t.Errorf("survivor after owner cleanup: %v", err)
			}
			// First terminal outcome remains cancellation after cleanup.
			if f.result(victim) != err {
				t.Error("cleanup replaced first terminal outcome")
			}
			f.m.End(bt)
			f.empty()
		})
	}
}

func TestRelationDeadlockSoftPublicationInterruption(t *testing.T) {
	f := newLockFixture(t)
	at, bt, ct := f.tx(), f.tx(), f.tx()
	x := f.relation()
	y := f.relation()
	f.request(at, x, AccessShare)
	f.request(ct, y, AccessExclusive)
	rb := f.request(bt, x, AccessExclusive)
	rc := f.request(ct, x, AccessShare)
	real, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	gate := &deadlockNormalizationContext{Context: real, observed: make(chan struct{}), resume: make(chan struct{}), armed: true}
	f.m.mu.Lock()
	ra := deadlockPublicationPending(f.m, at, y, AccessShare, context.Background())
	rb.ctx = gate
	// Actual arrival graph: B->A hard, C->B soft, A->C hard.
	// Moving compatible C-AS ahead of B-AX resolves it without revoking A-AS.
	queue := f.m.entries[x].pending
	topology := len(queue) == 2 && queue[0] == rb && queue[1] == rc && f.m.entries[x].holders[at].Mode == AccessShare && f.m.entries[y].holders[ct].Mode == AccessExclusive
	f.m.mu.Unlock()
	if !topology {
		t.Fatal("missing resolvable soft topology")
	}
	deadlockPublicationRun(t, f.m, ra, gate.observed, gate.resume, cancel)
	if err := f.result(rb); err != context.Canceled {
		t.Errorf("interrupted soft predecessor lost caller identity: %v", err)
	}
	if err := f.result(rc); err != nil {
		t.Errorf("compatible follower must grant from fresh live queues: %v", err)
	}
	f.m.mu.Lock()
	fresh := len(f.m.entries[x].pending) == 0 && f.m.entries[x].holders[at].Mode == AccessShare && f.m.entries[x].holders[ct].Mode == AccessShare && !ra.terminal
	f.m.mu.Unlock()
	if !fresh {
		t.Error("stale proposal, collateral victim, or missing compatible follower grant")
	}
	f.m.End(bt)
	f.m.End(ct)
	if err := f.result(ra); err != nil {
		t.Errorf("remaining participant must grant without 40P01: %v", err)
	}
	f.m.End(at)
	f.empty()
}

func TestRelationDeadlockDeadlinesDuringDetection(t *testing.T) {
	// Expected results are explicit policy expectations, not obtained by calling
	// relationWaitError: lock wins SQL ties; original caller wins a caller/SQL
	// tie; a strictly earlier SQL deadline wins over the later caller deadline.
	for _, name := range []string{"lock", "statement", "caller", "lock-statement-tie", "caller-lock-tie", "caller-statement-tie", "lock-before-caller", "statement-before-caller"} {
		t.Run(name, func(t *testing.T) {
			f := newLockFixture(t)
			at, bt := f.tx(), f.tx()
			ref := f.relation()
			f.request(at, ref, AccessShare)
			f.request(bt, ref, AccessShare)
			caller := &deadlockDeadlineContext{Context: context.Background()}
			gate := &deadlockNormalizationContext{Context: context.Background(), observed: make(chan struct{}), resume: make(chan struct{}), armed: true}
			f.m.mu.Lock()
			interrupted := deadlockPublicationPending(f.m, at, ref, AccessExclusive, gate)
			interrupted.policy.Source = caller
			later := deadlockPublicationPending(f.m, bt, ref, AccessExclusive, context.Background())
			f.m.mu.Unlock()
			code, message := "", ""
			deadlockPublicationRun(t, f.m, interrupted, gate.observed, gate.resume, func() {
				// Advance the existing absolute-deadline seams AFTER the detector's
				// first nil observation. The held mutex excludes timer callbacks.
				expired := time.Now().Add(-time.Second)
				switch name {
				case "lock", "lock-statement-tie", "lock-before-caller":
					interrupted.deadline = expired
					code, message = "55P03", "canceling statement due to lock timeout"
					if name == "lock-statement-tie" {
						interrupted.policy.StatementDeadline = expired
					}
					if name == "lock-before-caller" {
						caller.deadline = expired.Add(time.Millisecond)
					}
				case "statement", "statement-before-caller":
					interrupted.policy.StatementDeadline = expired
					code, message = "57014", "canceling statement due to statement timeout"
					if name == "statement-before-caller" {
						caller.deadline = expired.Add(time.Millisecond)
					}
				case "caller", "caller-lock-tie", "caller-statement-tie":
					caller.deadline = expired
					if name == "caller-lock-tie" {
						interrupted.deadline = expired
					}
					if name == "caller-statement-tie" {
						interrupted.policy.StatementDeadline = expired
					}
				}
			})
			err := f.result(interrupted)
			if code != "" {
				f.diagnostic(err, code, message)
			} else {
				var original SourceError
				var diagnostic *sqlstate.Error
				if !errors.As(err, &original) || original.error != context.DeadlineExceeded || !errors.Is(err, context.DeadlineExceeded) || errors.As(err, &diagnostic) {
					t.Errorf("caller deadline must retain original identity without SQLSTATE: %v", err)
				}
			}
			f.m.mu.Lock()
			collateral := later.terminal
			holds := f.m.entries[ref].holders[at].Mode == AccessShare && f.m.entries[ref].holders[bt].Mode == AccessShare
			f.m.mu.Unlock()
			if collateral || !holds {
				t.Errorf("deadline caused collateral terminal=%v or released holds=%v", collateral, !holds)
			}
			f.m.End(at)
			if err := f.result(later); err != nil {
				t.Errorf("deadline survivor: %v", err)
			}
			f.m.End(bt)
			f.empty()
		})
	}
}
