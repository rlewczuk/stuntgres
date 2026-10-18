package engine

import (
	"maps"
	"slices"
	"time"

	"stuntgres/internal/engine/ids"
)

// Diagnostic access for tests and tooling. Each method takes only the manager
// mutex, runs no callbacks, and returns owned values. The only state shared
// with the manager is immutable identity: ticket and request pointers and
// interned unique-key values.

// lockSnapshot is an owned, point-in-time copy of manager state.
type lockSnapshot struct {
	Epoch     ids.CatalogEpoch
	Owners    map[ids.TxRef]*relationTicket        // registered transaction tickets
	Sessions  map[ids.SessionToken]*relationTicket // registered session tickets
	Resources map[lockResource]*lockResourceState
	// Journals holds each ticket's resource journal: the resources it holds or
	// waits on. It covers every registered, holding or waiting ticket and the
	// tickets passed to Inspect; a journal never allocated is nil.
	Journals   map[*relationTicket]map[lockResource]struct{}
	Detached   map[*relationTicket]bool // prepared tickets among Journals' keys
	KeyBuckets int                      // live unique-key buckets

	MultiNext    uint32                      // next MultiXact ID to allocate
	MultiHistory int                         // retained MultiXact member sets
	MultiRows    map[multiTuple]lockMultiRow // current physical row status
	// Contributions holds each Journals ticket's physical status bits by tuple.
	Contributions map[*relationTicket]map[multiTuple]uint8
	// UniqueDeltas holds the unique-key claims each Journals ticket's active
	// conflict-update candidate newly acquired.
	UniqueDeltas map[*relationTicket][]lockResource
}

// lockMultiRow is a copy of one physical row version's status.
type lockMultiRow struct {
	ID      uint32 // MultiXact ID; zero for a single member
	Members []multiMember
}

// lockResourceState is one resource's grants and queue.
type lockResourceState struct {
	Holders map[*relationTicket]relationHold
	Pending []lockWaiter // queue order; may include terminal requests not yet settled
	Pins    int          // unique-key pins reserving the resource
}

// lockWaiter is a copy of one queued request.
type lockWaiter struct {
	Request    *relationRequest
	Ticket     *relationTicket
	Provenance ids.TxRef // the transaction a session-ticket request belongs to
	Resource   lockResource
	Mode       relationLockMode
	Terminal   bool
	Policy     relationLockPolicy
	Deadline   time.Time // lock-timeout deadline; zero when untimed
	EnqueuedAt time.Time
}

// Inspect copies the manager state in one critical section. Pass tickets that
// may no longer be registered to include their journals.
func (m *relationLockManager) Inspect(tickets ...*relationTicket) lockSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := lockSnapshot{
		Epoch:      m.epoch,
		Owners:     maps.Clone(m.owners),
		Sessions:   maps.Clone(m.sessions),
		Resources:  make(map[lockResource]*lockResourceState, len(m.entries)),
		Journals:   make(map[*relationTicket]map[lockResource]struct{}),
		Detached:   make(map[*relationTicket]bool),
		KeyBuckets: len(m.keyBuckets),

		MultiNext:     m.multiNext,
		MultiHistory:  len(m.multiHistory),
		MultiRows:     make(map[multiTuple]lockMultiRow, len(m.multiRows)),
		Contributions: make(map[*relationTicket]map[multiTuple]uint8),
		UniqueDeltas:  make(map[*relationTicket][]lockResource),
	}
	for tuple, row := range m.multiRows {
		s.MultiRows[tuple] = lockMultiRow{ID: row.id, Members: slices.Clone(row.members)}
	}
	if s.Owners == nil {
		s.Owners = make(map[ids.TxRef]*relationTicket)
	}
	if s.Sessions == nil {
		s.Sessions = make(map[ids.SessionToken]*relationTicket)
	}
	journal := func(t *relationTicket) {
		if _, ok := s.Journals[t]; t == nil || ok {
			return
		}
		s.Journals[t] = maps.Clone(t.resources) // nil when never allocated
		if t.detached {
			s.Detached[t] = true
		}
		if len(t.multiContributions) != 0 {
			s.Contributions[t] = maps.Clone(t.multiContributions)
		}
		if t.uniqueDeltaActive {
			s.UniqueDeltas[t] = slices.Clone(t.uniqueDelta)
		}
	}
	for _, t := range m.owners {
		journal(t)
	}
	for _, t := range m.sessions {
		journal(t)
	}
	for _, t := range tickets {
		journal(t)
	}
	for resource, entry := range m.entries {
		state := &lockResourceState{Holders: maps.Clone(entry.holders), Pins: entry.pins}
		for t := range entry.holders {
			journal(t)
		}
		for _, r := range entry.pending {
			journal(r.ticket)
			state.Pending = append(state.Pending, lockWaiter{
				Request: r, Ticket: r.ticket, Provenance: r.provenance, Resource: r.ref, Mode: r.mode,
				Terminal: r.terminal, Policy: r.policy, Deadline: r.deadline, EnqueuedAt: r.enqueuedAt,
			})
		}
		s.Resources[resource] = state
	}
	return s
}

// MutexHeld reports whether some goroutine holds the manager mutex at this
// instant. Tests use it to assert that callbacks run outside the mutex.
func (m *relationLockManager) MutexHeld() bool {
	if m.mu.TryLock() {
		m.mu.Unlock()
		return false
	}
	return true
}

// Valid reports whether t was a registered live ticket.
func (s lockSnapshot) Valid(t *relationTicket) bool {
	if t == nil {
		return false
	}
	if t.persistent {
		return s.Sessions[t.session] == t
	}
	return s.Owners[t.ref] == t
}

// Participant is t's deadlock and blocking participant: its session, or its
// own transaction once prepared.
func (s lockSnapshot) Participant(t *relationTicket) lockParticipant {
	if s.Detached[t] {
		return lockParticipant{Prepared: t.ref}
	}
	return lockParticipant{Session: t.session}
}

// ExpireWait gives a queued request the outcome its deadlines produce at the
// instant at, then settles its queue, exactly as its timer would. It returns
// that outcome, or nil when the request is already terminal or nothing is due
// at at. Tests use it to expire a wait without timing.
func (m *relationLockManager) ExpireWait(r *relationRequest, at time.Time) error {
	var notify *relationRequest
	m.mu.Lock()
	var err error
	if !r.terminal && r.ticket.manager == m {
		if err = r.timeoutWaitError(at); err != nil {
			terminalRequest(r, err, &notify)
			m.settle(r.entry, &notify)
			m.retire(r.ref, r.entry)
		}
	}
	m.mu.Unlock()
	notifyRequests(notify)
	return err
}

// ExpireLockTimeout ends a queued request with the lock-timeout outcome, as
// though its lock timeout had fired first, whether or not the request has a
// lock deadline. It uses the same terminal path as ExpireWait and does
// nothing once the request is terminal.
func (m *relationLockManager) ExpireLockTimeout(r *relationRequest) {
	var notify *relationRequest
	m.mu.Lock()
	if !r.terminal && r.ticket.manager == m {
		terminalRequest(r, relationTimeoutError(relationTimeoutLock), &notify)
		m.settle(r.entry, &notify)
		m.retire(r.ref, r.entry)
	}
	m.mu.Unlock()
	notifyRequests(notify)
}

// Terminal reports whether the request has its final outcome.
func (r *relationRequest) Terminal() bool {
	m := r.ticket.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	return r.terminal
}

// Ticket is the ticket the request acquires for.
func (r *relationRequest) Ticket() *relationTicket { return r.ticket }

// Resource is the resource the request waits for.
func (r *relationRequest) Resource() lockResource { return r.ref }

// Deadline is the request's lock-timeout deadline; zero when untimed.
func (r *relationRequest) Deadline() time.Time { return r.deadline }

// Released reports that the request is terminal and that its lock timer and
// context callback are disarmed. Probing disarms a registration that was
// still armed, so a false result stays false.
func (r *relationRequest) Released() bool {
	terminal := r.Terminal()
	timerArmed := r.timer != nil && r.timer.Stop()
	callbackArmed := r.stop != nil && r.stop()
	return terminal && !timerArmed && !callbackArmed
}

// CompletionOf is the resource a waiter acquires to wait for t to finish.
func CompletionOf(t *relationTicket) lockResource { return lockResource{completion: t} }

// Completion is the ticket whose completion a completion resource waits for.
func (r lockResource) Completion() *relationTicket { return r.completion }

// Key is the interned unique key of a key resource; nil otherwise.
func (r lockResource) Key() *uniqueLockKey { return r.key }

// Value is the key's owned value, interpreted only by its comparer.
func (k *uniqueLockKey) Value() any { return k.value }

// Request is the queued timeout-tracked request; nil when none is queued.
func (slot *relationTimeoutSlot) Request() *relationRequest { return slot.request.Load() }

// Failed reports that a tracked request ended with a SQL timeout ERROR.
func (slot *relationTimeoutSlot) Failed() bool { return slot.failed.Load() }
