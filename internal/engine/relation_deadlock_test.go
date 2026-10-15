package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func deadlockRequest(t *testing.T, e *Engine, ticket *relationTicket, resource lockResource, mode relationLockMode) *relationRequest {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	r, err := e.locks.startAcquire(ctx, ticket, resource, mode, relationLockPolicy{completionPurpose: completionVirtual})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func deadlockResult(t *testing.T, r *relationRequest) error {
	t.Helper()
	if r == nil {
		return nil
	}
	select {
	case <-r.done:
		return r.err
	case <-time.After(time.Second):
		t.Fatal("request did not terminate before failure guard")
		return nil
	}
}
func deadlockDiagnostic(t *testing.T, err error, tickets []*relationTicket, resources []lockResource) {
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
		if resource.row != 0 {
			mode = "NoKeyUpdate"
			row = fmt.Sprintf(" row %d", resource.row)
		}
		line := fmt.Sprintf("Transaction %d (session %d, epoch %d) waits for %s on relation %d (class %d, sub-ID %d, epoch %d)%s; blocked by transaction %d (session %d, epoch %d).", ticket.ref.id, ticket.ref.session.id, ticket.ref.epoch, mode, resource.Address.ObjectID, resource.Address.ClassID, resource.Address.SubID, resource.Epoch, row, blocker.ref.id, blocker.ref.session.id, blocker.ref.epoch)
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
			e := New()
			n := 2
			if kind == "three" {
				n = 3
			}
			sessions := make([]*Session, n)
			tickets := make([]*relationTicket, n)
			resources := make([]lockResource, n)
			for i := range n {
				sessions[i] = sessionStart(t, e, nil)
				sessionSQL(t, sessions[i], fmt.Sprintf("CREATE TABLE t%d(v INT)", i))
				transactionBegin(t, sessions[i])
				tickets[i] = sessions[i].transactions.ticket
				resources[i] = lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), fmt.Sprintf("t%d", i))}
				t.Cleanup(func() { e.locks.end(tickets[i]) })
			}
			modes := make([]relationLockMode, n)
			for i := range n {
				modes[i] = accessExclusive
				if kind == "row" || kind == "mixed" && i == 1 {
					resources[i].row = RowID(i + 1)
					modes[i] = noKeyUpdate
				}
				if r := deadlockRequest(t, e, tickets[i], resources[i], modes[i]); r != nil {
					t.Fatal("uncontended hold queued")
				}
			}
			pending := make([]*relationRequest, n)
			for i := range n {
				pending[i] = deadlockRequest(t, e, tickets[i], resources[(i+1)%n], modes[(i+1)%n])
				if pending[i] == nil {
					t.Fatal("conflicting request granted")
				}
				if i < n-1 {
					e.locks.mu.Lock()
					terminal := pending[i].terminal
					e.locks.mu.Unlock()
					if terminal {
						t.Fatal("acyclic prefix victimized")
					}
				}
			}
			victim := -1
			e.locks.mu.Lock()
			for i, r := range pending {
				if r.terminal {
					if victim != -1 {
						t.Error("more than one victim")
					}
					victim = i
				}
			}
			if victim >= 0 {
				for i, resource := range resources {
					if e.locks.entries[resource].holders[tickets[i]].mode == 0 {
						t.Error("manager released held lock before Finish")
					}
				}
			}
			e.locks.mu.Unlock()
			if victim < 0 {
				t.Fatal("hard cycle was not detected")
			}
			deadlockDiagnostic(t, deadlockResult(t, pending[victim]), tickets, resources)
			e.locks.end(tickets[victim])
			// End each newly unblocked owner to let the remaining chain drain.
			for step := 1; step < n; step++ {
				i := (victim - step + n) % n
				if err := deadlockResult(t, pending[i]); err != nil {
					t.Fatal("survivor", err)
				}
				e.locks.end(tickets[i])
			}
			lockEmpty(t, e)
		})
	}
}

func TestRelationDeadlockSoftQueues(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	c := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t1(v INT)")
	sessionSQL(t, a, "CREATE TABLE t2(v INT)")
	for _, s := range []*Session{a, b, c} {
		transactionBegin(t, s)
		ticket := s.transactions.ticket
		t.Cleanup(func() { e.locks.end(ticket) })
	}
	x := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "t1")}
	y := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "t2")}
	deadlockRequest(t, e, a.transactions.ticket, x, accessShare)
	deadlockRequest(t, e, c.transactions.ticket, y, accessExclusive)
	rb := deadlockRequest(t, e, b.transactions.ticket, x, accessExclusive)
	rc := deadlockRequest(t, e, c.transactions.ticket, x, accessShare)
	e.locks.mu.Lock()
	queue := e.locks.entries[x].pending
	ordered := len(queue) == 2 && queue[0] == rb && queue[1] == rc
	e.locks.mu.Unlock()
	if !ordered {
		t.Fatal("missing conflicting predecessor queue proof")
	}
	ra := deadlockRequest(t, e, a.transactions.ticket, y, accessShare)
	if err := deadlockResult(t, rc); err != nil {
		t.Fatal("resolvable soft cycle must not abort", err)
	}
	e.locks.mu.Lock()
	ah := e.locks.entries[x].holders[a.transactions.ticket].mode
	bt := rb.terminal
	e.locks.mu.Unlock()
	if ah != accessShare || bt {
		t.Fatal("reorder revoked holder or granted incompatible follower")
	}
	e.locks.end(c.transactions.ticket)
	if err := deadlockResult(t, ra); err != nil {
		t.Fatal(err)
	}
	e.locks.end(a.transactions.ticket)
	if err := deadlockResult(t, rb); err != nil {
		t.Fatal(err)
	}
	e.locks.end(b.transactions.ticket)
	lockEmpty(t, e)
}

func TestRelationDeadlockUncontended(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	transactionBegin(t, a)
	ticket := a.transactions.ticket
	defer e.locks.end(ticket)
	resource := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "t")}
	if err := e.locks.acquire(context.Background(), ticket, resource, accessShare); err != nil {
		t.Fatal(err)
	}
	allocations := testing.AllocsPerRun(100, func() {
		if err := e.locks.acquire(context.Background(), ticket, resource, accessShare); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("self reentry allocated %g times", allocations)
	}
}
