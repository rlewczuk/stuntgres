package core_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func deadlockRequest(t *testing.T, e *core.Engine, ticket *lock.Ticket, resource lock.Resource, mode lock.Mode) *lock.Request {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	r, err := e.Locks.StartAcquire(ctx, ticket, resource, mode, lock.Policy{CompletionPurpose: lock.CompletionVirtual})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func deadlockResult(t *testing.T, r *lock.Request) error {
	t.Helper()
	if r == nil {
		return nil
	}
	if deadlockGuardCompleted(r.Done()) {
		return r.Err()
	}
	guard := time.NewTimer(time.Second)
	defer guard.Stop()
	if !deadlockAwait(r.Done(), guard.C) {
		t.Fatal("request did not terminate before failure guard")
	}
	return r.Err()
}

func deadlockAwait(done <-chan struct{}, guard <-chan time.Time) bool {
	if deadlockGuardCompleted(done) {
		return true
	}
	select {
	case <-done:
		return true
	case <-guard:
		return deadlockGuardCompleted(done)
	}
}

func deadlockGuardCompleted(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}
func deadlockDiagnostic(t *testing.T, err error, tickets []*lock.Ticket, resources []lock.Resource) {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(err, &d) {
		t.Fatalf("want SQL diagnostic, got %v", err)
	}
	if d.Code != "40P01" || d.Message != "deadlock detected" || d.Severity != "ERROR" || d.SeverityUnlocalized != "ERROR" || d.Hint != "See server log for query details." || d.Position != "" || d.Context != "" {
		t.Fatalf("deadlock fields: %#v", d)
	}
	if d.InternalPosition != "" || d.InternalQuery != "" || d.Schema != "" || d.Table != "" || d.Column != "" || d.DataType != "" || d.Constraint != "" || d.File != "" || d.Line != "" || d.Routine != "" {
		t.Fatalf("unexpected unrelated fields %#v", d)
	}
	lines := strings.Split(strings.TrimSpace(d.Detail), "\n")
	if len(lines) != len(tickets) {
		t.Fatalf("cycle has %d edges, detail %q", len(tickets), d.Detail)
	}
	expected := make(map[string]bool, len(tickets))
	for i, ticket := range tickets {
		blocker := tickets[(i+1)%len(tickets)]
		resource := resources[(i+1)%len(resources)]
		mode := "AccessExclusiveLock"
		row := ""
		if resource.Row != 0 {
			mode = "NoKeyUpdate"
			row = fmt.Sprintf(" row %d", resource.Row)
		}
		line := fmt.Sprintf("Transaction %d (session %d, epoch %d) waits for %s on relation %d (class %d, sub-ID %d, epoch %d)%s; blocked by transaction %d (session %d, epoch %d).", ticket.Ref().ID(), ticket.Ref().Session().ID(), ticket.Ref().Epoch(), mode, resource.Address.ObjectID, resource.Address.ClassID, resource.Address.SubID, resource.Epoch, row, blocker.Ref().ID(), blocker.Ref().Session().ID(), blocker.Ref().Epoch())
		expected[line] = true
	}
	for _, line := range lines {
		if !expected[line] {
			t.Fatalf("edge lacks exact owned request/resource/blocker: %q", line)
		}
		delete(expected, line)
	}
	if len(expected) != 0 {
		t.Fatal("missing witnessed edge", expected)
	}
	if strings.Contains(d.Detail, "Process ") || strings.Contains(d.Detail, "tuple (") {
		t.Fatalf("fabricated physical identity: %q", d.Detail)
	}
}

func TestRelationDeadlockHardCycles(t *testing.T) {
	for _, kind := range []string{"table", "row", "mixed", "three"} {
		t.Run(kind, func(t *testing.T) {
			e := engine.New()
			n := 2
			if kind == "three" {
				n = 3
			}
			sessions := make([]*session.Session, n)
			tickets := make([]*lock.Ticket, n)
			resources := make([]lock.Resource, n)
			for i := range n {
				sessions[i] = enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, sessions[i], fmt.Sprintf("CREATE TABLE t%d(v INT)", i))
				enginetest.TransactionBegin(t, sessions[i])
				tickets[i] = sessions[i].Transactions.Ticket
				resources[i] = lock.Resource{ObjectRef: enginetest.CatalogRef(t, e, e.CatalogRead(), fmt.Sprintf("t%d", i))}
				t.Cleanup(func() { e.EndLockOwner(tickets[i]) })
			}
			modes := make([]lock.Mode, n)
			for i := range n {
				modes[i] = lock.AccessExclusive
				if kind == "row" || kind == "mixed" && i == 1 {
					resources[i].Row = ids.RowID(i + 1)
					modes[i] = lock.ForNoKeyUpdate
				}
				if r := deadlockRequest(t, e, tickets[i], resources[i], modes[i]); r != nil {
					t.Fatal("uncontended hold queued")
				}
			}
			pending := make([]*lock.Request, n)
			for i := range n {
				pending[i] = deadlockRequest(t, e, tickets[i], resources[(i+1)%n], modes[(i+1)%n])
				if pending[i] == nil {
					t.Fatal("conflicting request granted")
				}
				if i < n-1 {
					terminal := pending[i].Terminal()
					if terminal {
						t.Fatal("acyclic prefix victimized")
					}
				}
			}
			victim := -1
			lockState := e.Locks.Inspect()
			for i, r := range pending {
				if r.Terminal() {
					if victim != -1 {
						t.Error("more than one victim")
					}
					victim = i
				}
			}
			if victim >= 0 {
				for i, resource := range resources {
					if lockState.Resources[resource].Holders[tickets[i]].Mode == 0 {
						t.Error("manager released held lock before Finish")
					}
				}
			}
			if victim < 0 {
				t.Fatal("hard cycle was not detected")
			}
			deadlockDiagnostic(t, deadlockResult(t, pending[victim]), tickets, resources)
			e.EndLockOwner(tickets[victim])
			// End each newly unblocked owner to let the remaining chain drain.
			for step := 1; step < n; step++ {
				i := (victim - step + n) % n
				if err := deadlockResult(t, pending[i]); err != nil {
					t.Fatal("survivor", err)
				}
				e.EndLockOwner(tickets[i])
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestRelationDeadlockSoftQueues(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	c := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t1(v INT)")
	enginetest.SessionSQL(t, a, "CREATE TABLE t2(v INT)")
	for _, s := range []*session.Session{a, b, c} {
		enginetest.TransactionBegin(t, s)
		ticket := s.Transactions.Ticket
		t.Cleanup(func() { e.EndLockOwner(ticket) })
	}
	x := lock.Resource{ObjectRef: enginetest.CatalogRef(t, e, e.CatalogRead(), "t1")}
	y := lock.Resource{ObjectRef: enginetest.CatalogRef(t, e, e.CatalogRead(), "t2")}
	deadlockRequest(t, e, a.Transactions.Ticket, x, lock.AccessShare)
	deadlockRequest(t, e, c.Transactions.Ticket, y, lock.AccessExclusive)
	rb := deadlockRequest(t, e, b.Transactions.Ticket, x, lock.AccessExclusive)
	rc := deadlockRequest(t, e, c.Transactions.Ticket, x, lock.AccessShare)
	lockState := e.Locks.Inspect()
	queue := lockState.Resources[x].Pending
	ordered := len(queue) == 2 && queue[0].Request == rb && queue[1].Request == rc
	if !ordered {
		t.Fatal("missing conflicting predecessor queue proof")
	}
	ra := deadlockRequest(t, e, a.Transactions.Ticket, y, lock.AccessShare)
	if err := deadlockResult(t, rc); err != nil {
		t.Fatal("resolvable soft cycle must not abort", err)
	}
	lockState = e.Locks.Inspect()
	ah := lockState.Resources[x].Holders[a.Transactions.Ticket].Mode
	bt := rb.Terminal()
	if ah != lock.AccessShare || bt {
		t.Fatal("reorder revoked holder or granted incompatible follower")
	}
	e.EndLockOwner(c.Transactions.Ticket)
	if err := deadlockResult(t, ra); err != nil {
		t.Fatal(err)
	}
	e.EndLockOwner(a.Transactions.Ticket)
	if err := deadlockResult(t, rb); err != nil {
		t.Fatal(err)
	}
	e.EndLockOwner(b.Transactions.Ticket)
	enginetest.LockEmpty(t, e)
}

func TestRelationDeadlockUncontended(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
	enginetest.TransactionBegin(t, a)
	ticket := a.Transactions.Ticket
	defer e.EndLockOwner(ticket)
	resource := lock.Resource{ObjectRef: enginetest.CatalogRef(t, e, e.CatalogRead(), "t")}
	if err := e.Locks.Acquire(context.Background(), ticket, resource, lock.AccessShare); err != nil {
		t.Fatal(err)
	}
	allocations := testing.AllocsPerRun(100, func() {
		if err := e.Locks.Acquire(context.Background(), ticket, resource, lock.AccessShare); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("self reentry allocated %g times", allocations)
	}
}

// SQLSTATE must remain a SQL diagnostic through command admission translation.
func TestRelationDeadlockCommandErrorIdentity(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	ctx, cancel := context.WithCancel(context.Background())
	op, _, err := s.AcquireCommand(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	original := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40P01", Message: "deadlock detected"}
	cancel()
	if got := op.CommandError(original); got != original {
		t.Fatalf("genuine SQL error translated: %v", got)
	}
}
