package lock

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

// Snapshot is an owned, point-in-time copy of manager state.
type Snapshot struct {
	Epoch     ids.CatalogEpoch
	Owners    map[ids.TxRef]*Ticket        // registered transaction tickets
	Sessions  map[ids.SessionToken]*Ticket // registered session tickets
	Resources map[Resource]*ResourceState
	// Journals holds each ticket's resource journal: the resources it holds or
	// waits on. It covers every registered, holding or waiting ticket and the
	// tickets passed to Inspect; a journal never allocated is nil.
	Journals   map[*Ticket]map[Resource]struct{}
	Detached   map[*Ticket]bool // prepared tickets among Journals' keys
	KeyBuckets int              // live unique-key buckets

	MultiNext    uint32                  // next MultiXact ID to allocate
	MultiHistory int                     // retained MultiXact member sets
	MultiRows    map[MultiTuple]MultiRow // current physical row status
	// Contributions holds each Journals ticket's physical status bits by tuple.
	Contributions map[*Ticket]map[MultiTuple]uint8
	// UniqueDeltas holds the unique-key claims each Journals ticket's active
	// conflict-update candidate newly acquired.
	UniqueDeltas map[*Ticket][]Resource
}

// MultiRow is a copy of one physical row version's status.
type MultiRow struct {
	ID      uint32 // MultiXact ID; zero for a single member
	Members []MultiMember
}

// ResourceState is one resource's grants and queue.
type ResourceState struct {
	Holders map[*Ticket]Hold
	Pending []Waiter // queue order; may include terminal requests not yet settled
	Pins    int      // unique-key pins reserving the resource
}

// Waiter is a copy of one queued request.
type Waiter struct {
	Request    *relationRequest
	Ticket     *Ticket
	Provenance ids.TxRef // the transaction a session-ticket request belongs to
	Resource   Resource
	Mode       Mode
	Terminal   bool
	Policy     Policy
	Deadline   time.Time // lock-timeout deadline; zero when untimed
	EnqueuedAt time.Time
}

// Inspect copies the manager state in one critical section. Pass tickets that
// may no longer be registered to include their journals.
func (m *Manager) Inspect(tickets ...*Ticket) Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Snapshot{
		Epoch:      m.epoch,
		Owners:     maps.Clone(m.owners),
		Sessions:   maps.Clone(m.sessions),
		Resources:  make(map[Resource]*ResourceState, len(m.entries)),
		Journals:   make(map[*Ticket]map[Resource]struct{}),
		Detached:   make(map[*Ticket]bool),
		KeyBuckets: len(m.keyBuckets),

		MultiNext:     m.multiNext,
		MultiHistory:  len(m.multiHistory),
		MultiRows:     make(map[MultiTuple]MultiRow, len(m.multiRows)),
		Contributions: make(map[*Ticket]map[MultiTuple]uint8),
		UniqueDeltas:  make(map[*Ticket][]Resource),
	}
	for tuple, row := range m.multiRows {
		s.MultiRows[tuple] = MultiRow{ID: row.id, Members: slices.Clone(row.members)}
	}
	if s.Owners == nil {
		s.Owners = make(map[ids.TxRef]*Ticket)
	}
	if s.Sessions == nil {
		s.Sessions = make(map[ids.SessionToken]*Ticket)
	}
	journal := func(t *Ticket) {
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
		state := &ResourceState{Holders: maps.Clone(entry.holders), Pins: entry.pins}
		for t := range entry.holders {
			journal(t)
		}
		for _, r := range entry.pending {
			journal(r.ticket)
			state.Pending = append(state.Pending, Waiter{
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
func (m *Manager) MutexHeld() bool {
	if m.mu.TryLock() {
		m.mu.Unlock()
		return false
	}
	return true
}

// Valid reports whether t was a registered live ticket.
func (s Snapshot) Valid(t *Ticket) bool {
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
func (s Snapshot) Participant(t *Ticket) Participant {
	if s.Detached[t] {
		return Participant{Prepared: t.ref}
	}
	return Participant{Session: t.session}
}

// ExpireWait gives a queued request the outcome its deadlines produce at the
// instant at, then settles its queue, exactly as its timer would. It returns
// that outcome, or nil when the request is already terminal or nothing is due
// at at. Tests use it to expire a wait without timing.
func (m *Manager) ExpireWait(r *relationRequest, at time.Time) error {
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
func (m *Manager) ExpireLockTimeout(r *relationRequest) {
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
func (r *relationRequest) Ticket() *Ticket { return r.ticket }

// Resource is the resource the request waits for.
func (r *relationRequest) Resource() Resource { return r.ref }

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
func CompletionOf(t *Ticket) Resource { return Resource{completion: t} }

// Completion is the ticket whose completion a completion resource waits for.
func (r Resource) Completion() *Ticket { return r.completion }

// Key is the interned unique key of a key resource; nil otherwise.
func (r Resource) Key() *UniqueKey { return r.key }

// Value is the key's owned value, interpreted only by its comparer.
func (k *UniqueKey) Value() any { return k.value }

// Request is the queued timeout-tracked request; nil when none is queued.
func (slot *TimeoutSlot) Request() *relationRequest { return slot.request.Load() }

// Failed reports that a tracked request ended with a SQL timeout ERROR.
func (slot *TimeoutSlot) Failed() bool { return slot.failed.Load() }
