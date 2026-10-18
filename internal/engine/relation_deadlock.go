package engine

import (
	"fmt"
	"strings"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// All scratch below belongs to one blocked acquisition under manager metadata.
// Authoritative ownership remains in entries/tickets; proposals never mutate it.
type deadlockEdge struct {
	from, to      int
	request       *relationRequest
	blocker       *relationTicket
	before, after int // soft edge reversal: request before its queued blocker
}
type deadlockQueue struct {
	entry    *relationEntry
	requests []*relationRequest
	ids      []int
	order    []int
	emitted  []bool
}
type deadlockConstraint struct{ before, after int }
type deadlockSearch struct {
	participants []lockParticipant
	nodes        map[lockParticipant]int
	queues       []deadlockQueue
	positions    []int
	edges        [][]deadlockEdge
	state        []uint8
	parent       []deadlockEdge
	cycle        []deadlockEdge
	hardCycle    []deadlockEdge
	constraints  []deadlockConstraint
	frames       [][]deadlockEdge
}

func (m *relationLockManager) detectDeadlock(newRequest *relationRequest, notify **relationRequest) {
	for {
		m.normalizeDeadlockWaits(notify)
		if newRequest.terminal || !m.detectDeadlockSnapshot(newRequest, notify) {
			return
		}
	}
}

// Reuse settle's canonical error arbitration and grant/journal ownership. Any
// removed request (including a grant) invalidates a snapshot's dependencies.
func (m *relationLockManager) normalizeDeadlockWaits(notify **relationRequest) bool {
	changed := false
	for ref, entry := range m.entries {
		count := len(entry.pending)
		for _, r := range entry.pending {
			if !r.terminal && !m.validRequest(r) {
				terminalRequest(r, ErrCatalogReplaced, notify)
			}
		}
		m.settle(entry, notify)
		m.retire(ref, entry)
		changed = changed || len(entry.pending) != count
	}
	return changed
}

// Return true only when normalization invalidated the decision and live graph
// reconstruction is required. Hypothetical search never mutates real queues.
func (m *relationLockManager) detectDeadlockSnapshot(newRequest *relationRequest, notify **relationRequest) bool {
	requestCount := 0
	s := deadlockSearch{nodes: make(map[lockParticipant]int)}
	node := func(ticket *relationTicket) {
		participant := ticket.participant()
		if _, ok := s.nodes[participant]; !ok {
			s.nodes[participant] = len(s.participants)
			s.participants = append(s.participants, participant)
		}
	}
	for _, entry := range m.entries {
		if len(entry.pending) == 0 {
			continue
		}
		q := deadlockQueue{entry: entry}
		for _, r := range entry.pending {
			if r.terminal || !m.validRequest(r) {
				continue
			}
			q.requests = append(q.requests, r)
			node(r.ticket)
			q.ids = append(q.ids, requestCount)
			requestCount++
		}
		// Live queues only contain exact registered tickets. Holders without waits
		// still need nodes, but have no outgoing dependencies.
		for ticket := range entry.holders {
			if m.valid(ticket) {
				node(ticket)
			}
		}
		s.queues = append(s.queues, q)
	}
	n := len(s.participants)
	s.positions = make([]int, requestCount)
	s.edges = make([][]deadlockEdge, n)
	s.state = make([]uint8, n)
	s.parent = make([]deadlockEdge, n)
	for _, q := range s.queues {
		for position, id := range q.ids {
			s.positions[id] = position
		}
	}
	if !s.findCycle() {
		return false
	}
	// Sorting and branch storage are needed only for a witnessed soft cycle.
	// Adjacency grows with actual edges and is reused across proposals.
	// Preserve a witness in the real arrival graph, not a failed hypothetical one.
	witness := append([]deadlockEdge(nil), s.cycle...)
	if s.resolve(0) {
		// Contexts/deadlines advance while metadata is held; callbacks are not
		// required to win arbitration. Never install a proposal from stale waits.
		if m.normalizeDeadlockWaits(notify) {
			return true
		}
		for _, q := range s.queues {
			for i, original := range q.order {
				q.entry.pending[i] = q.requests[original]
			}
		}
		for _, q := range s.queues {
			m.settle(q.entry, notify)
		}
		return false
	}
	// A hard cycle found in a proposal exists in every real queue ordering.
	// Prefer it to an initial soft cycle when a disconnected hard cycle prevented
	// an otherwise successful rearrangement.
	if len(s.hardCycle) != 0 {
		witness = s.hardCycle
	}
	victim := witness[0].request
	for _, edge := range witness {
		if edge.request == newRequest {
			victim = newRequest
			break
		}
	}
	diagnostic := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40P01", Message: "deadlock detected", Hint: "See server log for query details."}
	var detail strings.Builder
	for i, edge := range witness {
		if i != 0 {
			detail.WriteByte('\n')
		}
		r, blocker := edge.request, edge.blocker
		if r.ref.Kind != ordinaryResource {
			fmt.Fprintf(&detail, "Session %d waits for %s on advisory lock [%d,%d,%d]; blocked by session %d.", r.ticket.session.ID(), deadlockMode(r.mode), r.ref.High, r.ref.Low, r.ref.Kind, blocker.session.ID())
			continue
		}
		if r.ref.completion != nil {
			fmt.Fprintf(&detail, "Transaction %d (session %d, epoch %d) waits for transaction completion", r.ticket.ref.ID(), r.ticket.ref.Session().ID(), r.ticket.ref.Epoch())
		} else {
			kind := "relation"
			if r.ref.Address.ClassID == ids.TypeClassID {
				kind = "type"
			}
			fmt.Fprintf(&detail, "Transaction %d (session %d, epoch %d) waits for %s on %s %d (class %d, sub-ID %d, epoch %d)", r.ticket.ref.ID(), r.ticket.ref.Session().ID(), r.ticket.ref.Epoch(), deadlockMode(r.mode), kind, r.ref.Address.ObjectID, r.ref.Address.ClassID, r.ref.Address.SubID, r.ref.Epoch)
			if r.ref.Row != 0 {
				fmt.Fprintf(&detail, " row %d", r.ref.Row)
			}
			if r.ref.key != nil {
				detail.WriteString(" unique key")
			}
		}
		fmt.Fprintf(&detail, "; blocked by transaction %d (session %d, epoch %d).", blocker.ref.ID(), blocker.ref.Session().ID(), blocker.ref.Epoch())
	}
	diagnostic.Detail = detail.String()
	// Formatting is also diagnostic work during which interruption can become
	// effective. Normalize immediately before publishing the victim decision.
	if m.normalizeDeadlockWaits(notify) {
		return true
	}
	// A later queue observation can interrupt this victim after its sweep check.
	// Arbitrate at the exact terminal boundary, just as settle does before grant.
	if err := WaitError(victim.ctx, victim.policy, victim.deadline, time.Now()); err != nil {
		terminalRequest(victim, err, notify)
		m.settle(victim.entry, notify)
		m.retire(victim.ref, victim.entry)
		return true
	}
	// Finish alone discards writes and releases held locks. Only the selected
	// exact pending contribution is removed here; survivors cannot see its effects.
	terminalRequest(victim, diagnostic, notify)
	m.settle(victim.entry, notify)
	m.retire(victim.ref, victim.entry)
	return false
}

// Stable topological sorting keeps arrival order unless a cycle constraint
// requires a move. Every proposal is rebuilt from the original live snapshot.
func (s *deadlockSearch) propose() bool {
	for qi := range s.queues {
		q := &s.queues[qi]
		if q.order == nil {
			q.order = make([]int, len(q.ids))
			q.emitted = make([]bool, len(q.ids))
		}
		clear(q.emitted)
		for output := range q.order {
			chosen := -1
			for candidate, id := range q.ids {
				if q.emitted[candidate] {
					continue
				}
				ready := true
				for _, c := range s.constraints {
					if c.after == id {
						for predecessor, other := range q.ids {
							if other == c.before && !q.emitted[predecessor] {
								ready = false
								break
							}
						}
					}
					if !ready {
						break
					}
				}
				if ready {
					chosen = candidate
					break
				}
			}
			if chosen < 0 {
				return false
			}
			q.emitted[chosen] = true
			q.order[output] = chosen
			s.positions[q.ids[chosen]] = output
		}
	}
	return true
}

func (s *deadlockSearch) findCycle() bool {
	for i := range s.edges {
		s.edges[i] = s.edges[i][:0]
	}
	for _, q := range s.queues {
		for index, r := range q.requests {
			from := s.nodes[r.ticket.participant()]
			for ticket, held := range q.entry.holders {
				to, live := s.nodes[ticket.participant()]
				if live && !sameLockSession(ticket, r.ticket) && Conflicts(r.mode, held.Mode) {
					s.edges[from] = append(s.edges[from], deadlockEdge{from: from, to: to, request: r, blocker: ticket, before: -1, after: -1})
				}
			}
			for earlier, blocker := range q.requests {
				if r.ref.completion != nil {
					continue
				}
				if sameLockSession(blocker.ticket, r.ticket) || s.positions[q.ids[earlier]] >= s.positions[q.ids[index]] || !Conflicts(r.mode, blocker.mode) {
					continue
				}
				// A conflicting existing hold makes this dependency hard already.
				hard := false
				for holder, held := range q.entry.holders {
					if sameLockSession(holder, blocker.ticket) && Conflicts(r.mode, held.Mode) {
						hard = true
						break
					}
				}
				if hard {
					continue
				}
				s.edges[from] = append(s.edges[from], deadlockEdge{from: from, to: s.nodes[blocker.ticket.participant()], request: r, blocker: blocker.ticket, before: q.ids[index], after: q.ids[earlier]})
			}
		}
	}
	clear(s.state)
	s.cycle = s.cycle[:0]
	for node := range s.participants {
		if s.state[node] == 0 && s.visit(node) {
			return true
		}
	}
	return false
}
func (s *deadlockSearch) visit(node int) bool {
	s.state[node] = 1
	for _, edge := range s.edges[node] {
		if s.state[edge.to] == 1 {
			s.cycle = append(s.cycle, edge)
			for current := node; current != edge.to; current = s.parent[current].from {
				s.cycle = append(s.cycle, s.parent[current])
			}
			return true
		}
		if s.state[edge.to] == 0 {
			s.parent[edge.to] = edge
			if s.visit(edge.to) {
				return true
			}
		}
	}
	s.state[node] = 2
	return false
}

func (s *deadlockSearch) resolve(depth int) bool {
	// A frame retains only this cycle's soft alternatives while children reuse
	// DFS/sort scratch. Frame backing arrays are reused across sibling proposals.
	if depth == len(s.frames) {
		s.frames = append(s.frames, nil)
	}
	candidates := s.frames[depth][:0]
	for _, edge := range s.cycle {
		if edge.before >= 0 {
			candidates = append(candidates, edge)
		}
	}
	s.frames[depth] = candidates
	if len(candidates) == 0 {
		if len(s.hardCycle) == 0 {
			s.hardCycle = append([]deadlockEdge(nil), s.cycle...)
		}
		return false
	}
	for _, edge := range candidates {
		duplicate := false
		for _, c := range s.constraints {
			if c.before == edge.before && c.after == edge.after {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		s.constraints = append(s.constraints, deadlockConstraint{edge.before, edge.after})
		if s.propose() && (!s.findCycle() || s.resolve(depth+1)) {
			return true
		}
		s.constraints = s.constraints[:len(s.constraints)-1]
	}
	return false
}
func deadlockMode(mode relationLockMode) string {
	switch mode {
	case accessShare:
		return "AccessShareLock"
	case rowExclusive:
		return "RowExclusiveLock"
	case accessExclusive:
		return "AccessExclusiveLock"
	case shareUpdateExclusive:
		return "ShareUpdateExclusiveLock"
	case shareRowExclusive:
		return "ShareRowExclusiveLock"
	case uniqueClaim:
		return "UniqueKey"
	case rowUpdate:
		return "Update"
	case noKeyUpdate:
		return "NoKeyUpdate"
	case keyShare:
		return "KeyShare"
	case rowShare:
		return "Share"
	case relationRowShare:
		return "RowShareLock"
	case relationShare:
		return "ShareLock"
	case relationExclusive:
		return "ExclusiveLock"
	case advisoryShared:
		return "ShareLock"
	case advisoryExclusive:
		return "ExclusiveLock"
	case typeExclusive:
		return "ExclusiveLock"
	}
	panic("invalid deadlock request mode")
}
