package lock

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
	f := newLockFixture(t)
	tickets := make([]*Ticket, 4)
	for i := range tickets {
		tickets[i] = f.tx()
	}
	resources := []Resource{f.relation(), f.relation(), f.relation()}
	f.request(tickets[0], resources[0], AccessShare)
	f.request(tickets[2], resources[1], AccessShare)
	f.request(tickets[0], resources[2], AccessExclusive)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	var requests []*relationRequest
	f.m.mu.Lock()
	enqueue := func(owner, resource int, mode Mode) {
		entry := f.m.entries[resources[resource]]
		r := &relationRequest{ticket: tickets[owner], provenance: tickets[owner].Ref(), ctx: ctx, mode: mode, done: make(chan struct{}), ref: resources[resource], entry: entry}
		entry.pending = append(entry.pending, r)
		tickets[owner].resources[resources[resource]] = struct{}{}
		requests = append(requests, r)
	}
	enqueue(1, 0, AccessExclusive)
	enqueue(3, 1, AccessExclusive)
	enqueue(0, 1, AccessShare)
	enqueue(2, 2, AccessExclusive)
	f.m.mu.Unlock()
	requests = append(requests, f.request(tickets[2], resources[0], AccessShare))
	actual := make([]deadlockModelQueue, 3)
	f.m.mu.Lock()
	for i, resource := range resources {
		entry := f.m.entries[resource]
		for ticket, mode := range entry.holders {
			for owner, candidate := range tickets {
				if ticket == candidate {
					actual[i].holders = append(actual[i].holders, deadlockModelWait{owner, mode.Mode&AccessExclusive != 0})
				}
			}
		}
		for _, r := range entry.pending {
			for owner, candidate := range tickets {
				if r.ticket == candidate {
					actual[i].waits = append(actual[i].waits, deadlockModelWait{owner, r.mode&AccessExclusive != 0})
				}
			}
		}
	}
	for _, r := range requests {
		if r != nil && r.terminal && r.Err() != nil {
			t.Errorf("avoidable victim: %v", r.Err())
		}
	}
	f.m.mu.Unlock()
	if !deadlockModelAcyclic(actual, 4) {
		t.Fatal("accepted queue arrangement still has a cycle")
	}
	for _, ticket := range tickets {
		f.m.End(ticket)
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
	f.empty()
}
