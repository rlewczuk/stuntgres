package engine

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

func deadlockPublicationRun(t *testing.T, e *Engine, request *relationRequest, observed, resume chan struct{}, advance func()) {
	t.Helper()
	finished := make(chan struct{})
	go func() {
		var notify *relationRequest
		e.locks.mu.Lock()
		e.locks.detectDeadlock(request, &notify)
		e.locks.mu.Unlock()
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
// regression. This isolates detector observations from startAcquire/settle.
func deadlockPublicationPending(e *Engine, ticket *relationTicket, ref lockResource, mode relationLockMode, ctx context.Context) *relationRequest {
	entry := e.locks.entries[ref]
	r := &relationRequest{ticket: ticket, provenance: ticket.ref, ctx: ctx, mode: mode, done: make(chan struct{}), ref: ref, entry: entry}
	ticket.resources[ref] = struct{}{}
	entry.pending = append(entry.pending, r)
	return r
}

func TestRelationDeadlockFinalSweepInterruption(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller", true: "source"}[source], func(t *testing.T) {
			e := New()
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE conversions(v INT)")
			transactionBegin(t, a)
			transactionBegin(t, b)
			at, bt := a.transactions.ticket, b.transactions.ticket
			t.Cleanup(func() { e.locks.end(at); e.locks.end(bt) })
			ref := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "conversions")}
			deadlockRequest(t, e, at, ref, accessShare)
			deadlockRequest(t, e, bt, ref, accessShare)
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
			e.locks.mu.Lock()
			// One authoritative queue guarantees observation order, independently
			// of map iteration: both AS holders request AX conversions. Each AX
			// has a HARD dependency on the other owner's retained AS hold.
			victim := deadlockPublicationPending(e, at, ref, accessExclusive, victimContext)
			later := deadlockPublicationPending(e, bt, ref, accessExclusive, laterContext)
			if source {
				victimContext.Context = context.Background()
				victim.policy.source = real
			}
			e.locks.mu.Unlock()
			deadlockPublicationRun(t, e, victim, observed, resume, func() {
				cancel()
				if real.Err() != context.Canceled {
					t.Error("cancellation not effective during later final observation")
				}
			})
			if len(trace) != 2 || trace[0] != "victim-final" || trace[1] != "later-final" {
				t.Fatalf("wrong explicit final-sweep observation order: %v", trace)
			}
			err := deadlockResult(t, victim)
			if !errors.Is(err, context.Canceled) {
				t.Errorf("victim must retain cancellation, got %v", err)
			}
			var diagnostic *sqlstate.Error
			if errors.As(err, &diagnostic) {
				t.Errorf("interruption fabricated SQLSTATE %s", diagnostic.Code)
			}
			if source {
				var original relationSourceError
				if !errors.As(err, &original) || original.error != context.Canceled {
					t.Errorf("source identity lost: %v", err)
				}
			} else if err != context.Canceled {
				t.Errorf("caller error identity lost: %v", err)
			}
			e.locks.mu.Lock()
			collateral := later.terminal
			holds := e.locks.entries[ref].holders[at].mode == accessShare && e.locks.entries[ref].holders[bt].mode == accessShare
			e.locks.mu.Unlock()
			if collateral || !holds {
				t.Errorf("collateral terminal=%v, original holds retained=%v", collateral, holds)
			}
			e.locks.end(at)
			if err := deadlockResult(t, later); err != nil {
				t.Errorf("survivor after owner cleanup: %v", err)
			}
			// First terminal outcome remains cancellation after cleanup.
			if deadlockResult(t, victim) != err {
				t.Error("cleanup replaced first terminal outcome")
			}
			e.locks.end(bt)
			lockEmpty(t, e)
		})
	}
}

func TestRelationDeadlockSoftPublicationInterruption(t *testing.T) {
	e := New()
	a, b, c := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE x(v INT)")
	sessionSQL(t, a, "CREATE TABLE y(v INT)")
	for _, s := range []*Session{a, b, c} {
		transactionBegin(t, s)
		ticket := s.transactions.ticket
		t.Cleanup(func() { e.locks.end(ticket) })
	}
	at, bt, ct := a.transactions.ticket, b.transactions.ticket, c.transactions.ticket
	x := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "x")}
	y := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "y")}
	deadlockRequest(t, e, at, x, accessShare)
	deadlockRequest(t, e, ct, y, accessExclusive)
	rb := deadlockRequest(t, e, bt, x, accessExclusive)
	rc := deadlockRequest(t, e, ct, x, accessShare)
	real, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	gate := &deadlockNormalizationContext{Context: real, observed: make(chan struct{}), resume: make(chan struct{}), armed: true}
	e.locks.mu.Lock()
	ra := deadlockPublicationPending(e, at, y, accessShare, context.Background())
	rb.ctx = gate
	// Actual arrival graph: B->A hard, C->B soft, A->C hard.
	// Moving compatible C-AS ahead of B-AX resolves it without revoking A-AS.
	queue := e.locks.entries[x].pending
	topology := len(queue) == 2 && queue[0] == rb && queue[1] == rc && e.locks.entries[x].holders[at].mode == accessShare && e.locks.entries[y].holders[ct].mode == accessExclusive
	e.locks.mu.Unlock()
	if !topology {
		t.Fatal("missing resolvable soft topology")
	}
	deadlockPublicationRun(t, e, ra, gate.observed, gate.resume, cancel)
	if err := deadlockResult(t, rb); err != context.Canceled {
		t.Errorf("interrupted soft predecessor lost caller identity: %v", err)
	}
	if err := deadlockResult(t, rc); err != nil {
		t.Errorf("compatible follower must grant from fresh live queues: %v", err)
	}
	e.locks.mu.Lock()
	fresh := len(e.locks.entries[x].pending) == 0 && e.locks.entries[x].holders[at].mode == accessShare && e.locks.entries[x].holders[ct].mode == accessShare && !ra.terminal
	e.locks.mu.Unlock()
	if !fresh {
		t.Error("stale proposal, collateral victim, or missing compatible follower grant")
	}
	e.locks.end(bt)
	e.locks.end(ct)
	if err := deadlockResult(t, ra); err != nil {
		t.Errorf("remaining participant must grant without 40P01: %v", err)
	}
	e.locks.end(at)
	lockEmpty(t, e)
}

func TestRelationDeadlockDeadlinesDuringDetection(t *testing.T) {
	// Expected results are explicit policy expectations, not obtained by calling
	// relationWaitError: lock wins SQL ties; original caller wins a caller/SQL
	// tie; a strictly earlier SQL deadline wins over the later caller deadline.
	for _, name := range []string{"lock", "statement", "caller", "lock-statement-tie", "caller-lock-tie", "caller-statement-tie", "lock-before-caller", "statement-before-caller"} {
		t.Run(name, func(t *testing.T) {
			e := New()
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE deadlines(v INT)")
			transactionBegin(t, a)
			transactionBegin(t, b)
			at, bt := a.transactions.ticket, b.transactions.ticket
			t.Cleanup(func() { e.locks.end(at); e.locks.end(bt) })
			ref := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "deadlines")}
			deadlockRequest(t, e, at, ref, accessShare)
			deadlockRequest(t, e, bt, ref, accessShare)
			caller := &deadlockDeadlineContext{Context: context.Background()}
			gate := &deadlockNormalizationContext{Context: context.Background(), observed: make(chan struct{}), resume: make(chan struct{}), armed: true}
			e.locks.mu.Lock()
			interrupted := deadlockPublicationPending(e, at, ref, accessExclusive, gate)
			interrupted.policy.source = caller
			later := deadlockPublicationPending(e, bt, ref, accessExclusive, context.Background())
			e.locks.mu.Unlock()
			code, message := "", ""
			deadlockPublicationRun(t, e, interrupted, gate.observed, gate.resume, func() {
				// Advance the existing absolute-deadline seams AFTER the detector's
				// first nil observation. The held mutex excludes timer callbacks.
				expired := time.Now().Add(-time.Second)
				switch name {
				case "lock", "lock-statement-tie", "lock-before-caller":
					interrupted.deadline = expired
					code, message = "55P03", "canceling statement due to lock timeout"
					if name == "lock-statement-tie" {
						interrupted.policy.statementDeadline = expired
					}
					if name == "lock-before-caller" {
						caller.deadline = expired.Add(time.Millisecond)
					}
				case "statement", "statement-before-caller":
					interrupted.policy.statementDeadline = expired
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
						interrupted.policy.statementDeadline = expired
					}
				}
			})
			err := deadlockResult(t, interrupted)
			if code != "" {
				waitsDiagnostic(t, err, code, message)
			} else {
				var original relationSourceError
				var diagnostic *sqlstate.Error
				if !errors.As(err, &original) || original.error != context.DeadlineExceeded || !errors.Is(err, context.DeadlineExceeded) || errors.As(err, &diagnostic) {
					t.Errorf("caller deadline must retain original identity without SQLSTATE: %v", err)
				}
			}
			e.locks.mu.Lock()
			collateral := later.terminal
			holds := e.locks.entries[ref].holders[at].mode == accessShare && e.locks.entries[ref].holders[bt].mode == accessShare
			e.locks.mu.Unlock()
			if collateral || !holds {
				t.Errorf("deadline caused collateral terminal=%v or released holds=%v", collateral, !holds)
			}
			e.locks.end(at)
			if err := deadlockResult(t, later); err != nil {
				t.Errorf("deadline survivor: %v", err)
			}
			e.locks.end(bt)
			lockEmpty(t, e)
		})
	}
}
