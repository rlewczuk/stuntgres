package engine

import (
	"context"
	"testing"
	"time"
)

// Test-only reachability closure is deliberately independent of detector DFS
// and constraint sorting. It enumerates every queue order, not detector choices.
type deadlockModelWait struct {
	owner     int
	exclusive bool
}
type deadlockModelQueue struct {
	holders []deadlockModelWait
	waits   []deadlockModelWait
}

func deadlockModelAcyclic(queues []deadlockModelQueue, n int) bool {
	edges := make([][]bool, n)
	for i := range edges {
		edges[i] = make([]bool, n)
	}
	for _, q := range queues {
		for i, w := range q.waits {
			for _, h := range q.holders {
				if w.owner != h.owner && (w.exclusive || h.exclusive) {
					edges[w.owner][h.owner] = true
				}
			}
			for _, before := range q.waits[:i] {
				if w.owner != before.owner && (w.exclusive || before.exclusive) {
					edges[w.owner][before.owner] = true
				}
			}
		}
	}
	for k := range n {
		for i := range n {
			for j := range n {
				edges[i][j] = edges[i][j] || edges[i][k] && edges[k][j]
			}
		}
	}
	for i := range n {
		if edges[i][i] {
			return false
		}
	}
	return true
}
func deadlockModelResolvable(queues []deadlockModelQueue, n int) bool {
	var visit func(int) bool
	visit = func(index int) bool {
		if index == len(queues) {
			return deadlockModelAcyclic(queues, n)
		}
		waits := queues[index].waits
		var permute func(int) bool
		permute = func(i int) bool {
			if i == len(waits) {
				return visit(index + 1)
			}
			for j := i; j < len(waits); j++ {
				waits[i], waits[j] = waits[j], waits[i]
				found := permute(i + 1)
				waits[i], waits[j] = waits[j], waits[i]
				if found {
					return true
				}
			}
			return false
		}
		return permute(0)
	}
	return visit(0)
}

// Build an already-observed queue snapshot under metadata, including multiple
// private pending requests for one exact owner. The last real enqueue initiates
// detection. This exercises global rearrangement, not just its closing cycle.
func TestRelationDeadlockSoftGlobalSearch(t *testing.T) {
	model := []deadlockModelQueue{
		{holders: []deadlockModelWait{{0, false}}, waits: []deadlockModelWait{{1, true}, {2, false}}},
		{holders: []deadlockModelWait{{2, false}}, waits: []deadlockModelWait{{3, true}, {0, false}}},
		{holders: []deadlockModelWait{{0, true}}, waits: []deadlockModelWait{{2, true}}},
	}
	if deadlockModelAcyclic(model, 4) || !deadlockModelResolvable(model, 4) {
		t.Fatal("independent model must be cyclic but resolvable")
	}
	// Reversing only C before B on x leaves C->A->D->C on y/z.
	model[0].waits[0], model[0].waits[1] = model[0].waits[1], model[0].waits[0]
	if deadlockModelAcyclic(model, 4) {
		t.Fatal("first reversal must leave another cycle")
	}
	model[0].waits[0], model[0].waits[1] = model[0].waits[1], model[0].waits[0]
	e := New()
	sessions := make([]*Session, 4)
	tickets := make([]*relationTicket, 4)
	for i := range sessions {
		sessions[i] = sessionStart(t, e, nil)
		transactionBegin(t, sessions[i])
		tickets[i] = sessions[i].transactions.ticket
		ticket := tickets[i]
		t.Cleanup(func() { e.endLockOwner(ticket) })
	}
	// Catalog creation belongs to an independent idle session.
	creator := sessionStart(t, e, nil)
	resources := make([]lockResource, 3)
	for i, name := range []string{"x", "y", "z"} {
		sessionSQL(t, creator, "CREATE TABLE "+name+"(v INT)")
		resources[i] = lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), name)}
	}
	deadlockRequest(t, e, tickets[0], resources[0], accessShare)
	deadlockRequest(t, e, tickets[2], resources[1], accessShare)
	deadlockRequest(t, e, tickets[0], resources[2], accessExclusive)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	var requests []*relationRequest
	e.locks.mu.Lock()
	enqueue := func(owner, resource int, mode relationLockMode) {
		entry := e.locks.entries[resources[resource]]
		r := &relationRequest{ticket: tickets[owner], provenance: tickets[owner].Ref(), ctx: ctx, mode: mode, done: make(chan struct{}), ref: resources[resource], entry: entry}
		entry.pending = append(entry.pending, r)
		tickets[owner].resources[resources[resource]] = struct{}{}
		requests = append(requests, r)
	}
	enqueue(1, 0, accessExclusive)
	enqueue(3, 1, accessExclusive)
	enqueue(0, 1, accessShare)
	enqueue(2, 2, accessExclusive)
	e.locks.mu.Unlock()
	requests = append(requests, deadlockRequest(t, e, tickets[2], resources[0], accessShare))
	actual := make([]deadlockModelQueue, 3)
	e.locks.mu.Lock()
	for i, resource := range resources {
		entry := e.locks.entries[resource]
		for ticket, mode := range entry.holders {
			for owner, candidate := range tickets {
				if ticket == candidate {
					actual[i].holders = append(actual[i].holders, deadlockModelWait{owner, mode.Mode&accessExclusive != 0})
				}
			}
		}
		for _, r := range entry.pending {
			for owner, candidate := range tickets {
				if r.ticket == candidate {
					actual[i].waits = append(actual[i].waits, deadlockModelWait{owner, r.mode&accessExclusive != 0})
				}
			}
		}
	}
	for _, r := range requests {
		if r != nil && r.terminal && r.Err() != nil {
			t.Errorf("avoidable victim: %v", r.Err())
		}
	}
	e.locks.mu.Unlock()
	if !deadlockModelAcyclic(actual, 4) {
		t.Fatal("accepted queue arrangement still has a cycle")
	}
	for _, ticket := range tickets {
		e.endLockOwner(ticket)
	}
	for _, r := range requests {
		if r != nil {
			select {
			case <-r.Done():
			case <-time.After(time.Second):
				t.Fatal("ended request not notified")
			}
		}
	}
	lockEmpty(t, e)
}
