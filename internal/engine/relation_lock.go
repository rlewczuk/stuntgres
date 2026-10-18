package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

var errRelationOwnerEnded = errors.New("stuntgres: transaction ended")

var errRelationUnavailable = errors.New("stuntgres: relation lock unavailable")

type relationLockPolicy struct {
	NoWait            bool
	Timeout           time.Duration
	StatementDeadline time.Time
	Source            context.Context
	Provenance        ids.TxRef             // current statement owner for a persistent session request
	TimeoutSlot       *relationTimeoutSlot  // admitted command's pending-timeout projection; nil when untracked
	CompletionPurpose completionLockPurpose // observational identity; never changes arbitration
}

type relationSourceError struct{ error }

func (e relationSourceError) Unwrap() error { return e.error }

// WaitError is the outcome a wait has at now: absolute SQL deadlines, not
// callback scheduling, decide it. Nil means the wait may continue.
func WaitError(ctx context.Context, policy relationLockPolicy, lockDeadline, now time.Time) error {
	if cause := relationTimeoutSourceCause(policy, lockDeadline, now); cause != nil {
		return relationSourceError{cause}
	}
	if err := relationTimeoutError(relationTimeoutWinner(policy, lockDeadline, now)); err != nil {
		return err
	}
	if cause := context.Cause(ctx); cause != nil {
		var fatal *sqlstate.Error
		if errors.As(cause, &fatal) && fatal.Severity == "FATAL" {
			copy := *fatal
			return &copy
		}
	}
	return ctx.Err()
}

type relationLockMode uint32

const (
	accessShare relationLockMode = 1 << iota
	rowExclusive
	accessExclusive
	shareUpdateExclusive
	rowUpdate
	noKeyUpdate
	shareRowExclusive
	keyShare
	rowShare
	relationRowShare
	relationShare
	relationExclusive
	uniqueClaim
	transactionCompletion
	advisoryShared
	advisoryExclusive
	typeExclusive
)

type lockResourceKind uint8

const (
	ordinaryResource lockResourceKind = iota
	advisoryBigintResource
	advisoryPairResource
)

// Row zero is the relation resource; logical rows use their stable RowID.
type lockResource struct {
	ids.ObjectRef
	Kind       lockResourceKind
	High, Low  uint32
	Row        ids.RowID
	key        *uniqueLockKey
	completion *relationTicket
}
type relationTicket struct {
	manager            *relationLockManager
	ref                ids.TxRef
	session            ids.SessionToken
	persistent         bool
	detached           bool // prepared transaction: independent of its former physical session
	resources          map[lockResource]struct{}
	assignedXID        uint32 // sync/atomic only: canonical TX mirror; end or completed index phase retires ownership
	fullXID            uint64 // sync/atomic only: exact canonical phase identity, not an allocator.
	ended              uint32 // sync/atomic only: ownership retirement, readable without manager.mu.
	completion         uint32 // sync/atomic only: transactionIdentityState before ownership release.
	multiContributions map[multiTuple]uint8
	multiCache         []uint32
	uniqueDeltaActive  bool
	uniqueDelta        []lockResource
}

// Ref is the transaction a ticket belongs to; zero for a session ticket.
func (t *relationTicket) Ref() ids.TxRef { return t.ref }

// Session is the session that owns the ticket.
func (t *relationTicket) Session() ids.SessionToken { return t.session }

// Persistent reports a session-lifetime (advisory) ticket.
func (t *relationTicket) Persistent() bool { return t.persistent }

func (t *relationTicket) AssignedPublicXID() uint32 { return atomic.LoadUint32(&t.assignedXID) }
func (t *relationTicket) MirrorFullXID(xid uint64) {
	atomic.StoreUint64(&t.fullXID, xid)
	atomic.StoreUint32(&t.assignedXID, uint32(xid))
}
func (t *relationTicket) AssignedFullXID() uint64 { return atomic.LoadUint64(&t.fullXID) }
func (t *relationTicket) OwnershipEnded() bool    { return atomic.LoadUint32(&t.ended) != 0 }
func (t *relationTicket) retireOwnership()        { atomic.StoreUint32(&t.ended, 1) }
func (t *relationTicket) CompletionState() ids.TxCompletion {
	return ids.TxCompletion(atomic.LoadUint32(&t.completion))
}
func (t *relationTicket) MirrorCompletion(state ids.TxCompletion) {
	atomic.StoreUint32(&t.completion, uint32(state))
}

type relationRequest struct {
	ticket       *relationTicket
	provenance   ids.TxRef
	ctx          context.Context
	mode         relationLockMode
	done         chan struct{}
	err          error
	terminal     bool
	notifyNext   *relationRequest
	stop         func() bool
	timer        *time.Timer
	policy       relationLockPolicy
	deadline     time.Time
	enqueuedAt   time.Time
	ref          lockResource
	entry        *relationEntry
	timeoutState atomic.Uint32
}

// Ordinary modes remain idempotent; only advisory grants have counted modes.
type relationHold struct {
	Mode              relationLockMode
	Shared, Exclusive uint64
}

type relationEntry struct {
	holders map[*relationTicket]relationHold
	pending []*relationRequest
	pins    int
}

// relationLockManager owns heavyweight lock state: transaction and session
// tickets, resource queues and grants, unique-key buckets, and MultiXact rows
// and history. mu guards all of it; engine code reaches it only through
// manager methods.
//
// Lock order (audited in engine-split step 4a). mu is innermost:
//   - It may be taken while holding replacementMu (admission reservation) or
//     a transactionOwner.mu (savepoint checkpoint and restore, finish).
//   - It is never taken while holding Engine.mu, Session.mu,
//     sessionSettings.mu or Engine.sessionMu. Callers release Engine.mu
//     before calling the manager.
//   - While mu is held, no other mutex is taken, no callback runs and nothing
//     blocks. A queued request waits on its done channel after mu is
//     released, and notifyRequests closes those channels after unlock.
//     Context and timer interrupts and unique-key unpin closures run with no
//     other mutex held.
//
// For reference, the surrounding engine order takes replacementMu and
// transactionOwner.mu before Engine.mu, and Engine.mu before Session.mu,
// sessionSettings.mu and Engine.sessionMu.
type relationLockManager struct {
	mu           sync.Mutex
	domain       *ids.Domain // Owning engine's identity domain; provenance checks only.
	epoch        ids.CatalogEpoch
	owners       map[ids.TxRef]*relationTicket
	sessions     map[ids.SessionToken]*relationTicket
	entries      map[lockResource]*relationEntry
	keyBuckets   map[uniqueLockBucket]*uniqueLockBucketState
	multiNext    uint32
	multiRows    map[multiTuple]multiRow
	multiHistory map[uint32][]multiMember
}

func newRelationLockManager(domain *ids.Domain) *relationLockManager {
	return &relationLockManager{domain: domain, multiNext: 1, owners: make(map[ids.TxRef]*relationTicket), entries: make(map[lockResource]*relationEntry)}
}

// Register admits a transaction ticket. The engine has already checked that
// ref is attached to its transaction owner and session (txOwnerAttached).
func (m *relationLockManager) Register(ref ids.TxRef) (*relationTicket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ref.Domain() != m.domain || ref.ID() == 0 || ref.Epoch() == 0 || ref.Attachment() == nil || ref.Session().Domain() != m.domain {
		return nil, errors.New("stuntgres: invalid lock owner")
	}
	if ref.Epoch() < m.epoch {
		return nil, ErrCatalogReplaced
	}
	if _, ok := m.owners[ref]; ok {
		return nil, errors.New("stuntgres: duplicate lock owner")
	}
	t := &relationTicket{manager: m, ref: ref, session: ref.Session(), resources: make(map[lockResource]struct{})}
	m.owners[ref] = t
	return t, nil
}
func Conflicts(a, b relationLockMode) bool {
	if a&typeExclusive != 0 || b&typeExclusive != 0 {
		return a&typeExclusive != 0 && b&typeExclusive != 0
	}
	if a&(advisoryShared|advisoryExclusive) != 0 || b&(advisoryShared|advisoryExclusive) != 0 {
		return a&advisoryExclusive != 0 && b&(advisoryShared|advisoryExclusive) != 0 ||
			b&advisoryExclusive != 0 && a&(advisoryShared|advisoryExclusive) != 0
	}
	if a&transactionCompletion != 0 || b&transactionCompletion != 0 {
		return a&transactionCompletion != 0 && b&transactionCompletion != 0
	}
	if a&uniqueClaim != 0 || b&uniqueClaim != 0 {
		return a&uniqueClaim != 0 && b&uniqueClaim != 0
	}
	rowModes := keyShare | rowShare | noKeyUpdate | rowUpdate
	if a&rowModes != 0 || b&rowModes != 0 {
		return a&rowUpdate != 0 && b&rowModes != 0 || b&rowUpdate != 0 && a&rowModes != 0 ||
			a&noKeyUpdate != 0 && b&(rowShare|noKeyUpdate) != 0 ||
			b&noKeyUpdate != 0 && a&(rowShare|noKeyUpdate) != 0
	}
	if a&accessExclusive != 0 || b&accessExclusive != 0 {
		return a != 0 && b != 0
	}
	exclusiveConflicts := relationRowShare | rowExclusive | shareUpdateExclusive | relationShare | shareRowExclusive | relationExclusive
	shareConflicts := rowExclusive | shareUpdateExclusive | shareRowExclusive | relationExclusive
	srxConflicts := rowExclusive | shareUpdateExclusive | relationShare | shareRowExclusive | relationExclusive
	return a&relationExclusive != 0 && b&exclusiveConflicts != 0 ||
		b&relationExclusive != 0 && a&exclusiveConflicts != 0 ||
		a&relationShare != 0 && b&shareConflicts != 0 ||
		b&relationShare != 0 && a&shareConflicts != 0 ||
		a&shareRowExclusive != 0 && b&srxConflicts != 0 ||
		b&shareRowExclusive != 0 && a&srxConflicts != 0 ||
		a&shareUpdateExclusive != 0 && b&shareUpdateExclusive != 0
}
func (m *relationLockManager) valid(t *relationTicket) bool {
	return t != nil && t.manager == m && (t.persistent && m.sessions[t.session] == t || !t.persistent && m.owners[t.ref] == t)
}

// A checkpoint contains only held contributions, never pending requests.
type relationCheckpointHold struct {
	Mode  relationLockMode
	Multi map[ids.VersionID]uint8
}
type relationLockCheckpoint map[lockResource]relationCheckpointHold

func (m *relationLockManager) Checkpoint(t *relationTicket) relationLockCheckpoint {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.valid(t) || t.persistent {
		return nil
	}
	held := make(relationLockCheckpoint, len(t.resources))
	for ref := range t.resources {
		if ref.completion != nil || ref.Kind != ordinaryResource && ref.Kind != advisoryBigintResource && ref.Kind != advisoryPairResource {
			continue
		}
		if entry := m.entries[ref]; entry != nil && entry.holders[t].Mode != 0 {
			held[ref] = relationCheckpointHold{Mode: entry.holders[t].Mode}
		}
	}
	for tuple, bits := range t.multiContributions {
		h := held[tuple.lockResource]
		if h.Multi == nil {
			h.Multi = make(map[ids.VersionID]uint8)
		}
		h.Multi[tuple.Version] = bits
		held[tuple.lockResource] = h
	}
	return held
}

// Caller serializes the owner but holds no catalog/session/publication locks.
// Intersecting prevents a stale checkpoint from resurrecting ownership.
func (m *relationLockManager) Restore(t *relationTicket, held relationLockCheckpoint) {
	var notify *relationRequest
	m.mu.Lock()
	if m.valid(t) && !t.persistent {
		m.restoreMultiContributionsLocked(t, held)
		for ref := range t.resources {
			if ref.completion != nil || ref.Kind != ordinaryResource && ref.Kind != advisoryBigintResource && ref.Kind != advisoryPairResource {
				continue
			}
			entry := m.entries[ref]
			if entry == nil {
				continue
			}
			mode := entry.holders[t].Mode & held[ref].Mode
			if mode == entry.holders[t].Mode {
				continue
			}
			if mode == 0 {
				delete(entry.holders, t)
				delete(t.resources, ref)
			} else {
				entry.holders[t] = relationHold{Mode: mode}
			}
			m.settle(entry, &notify)
			m.retire(ref, entry)
		}
	}
	m.mu.Unlock()
	notifyRequests(notify)
}

// Candidate omission changes only key contributions, not source row/relation
// grants. Reuse caller-owned scratch and preserve preexisting stronger modes.
func (m *relationLockManager) CheckpointUnique(t *relationTicket, held relationLockCheckpoint) relationLockCheckpoint {
	m.mu.Lock()
	defer m.mu.Unlock()
	if held == nil {
		held = make(relationLockCheckpoint)
	}
	clear(held)
	if m.valid(t) && !t.persistent {
		for ref := range t.resources {
			if ref.key != nil && ref.Kind == ordinaryResource && ref.completion == nil {
				if entry := m.entries[ref]; entry != nil && entry.holders[t].Mode != 0 {
					held[ref] = relationCheckpointHold{Mode: entry.holders[t].Mode}
				}
			}
		}
	}
	return held
}
func (m *relationLockManager) RestoreUnique(t *relationTicket, held relationLockCheckpoint) {
	var notify *relationRequest
	m.mu.Lock()
	if m.valid(t) && !t.persistent {
		for ref := range t.resources {
			if ref.key == nil || ref.Kind != ordinaryResource || ref.completion != nil {
				continue
			}
			entry := m.entries[ref]
			if entry == nil {
				continue
			}
			hold := entry.holders[t]
			mode := hold.Mode & held[ref].Mode
			if mode == hold.Mode {
				continue
			}
			if mode == 0 {
				delete(entry.holders, t)
				delete(t.resources, ref)
			} else {
				hold.Mode = mode
				entry.holders[t] = hold
			}
			m.settle(entry, &notify)
			m.retire(ref, entry)
		}
	}
	m.mu.Unlock()
	notifyRequests(notify)
}
func terminalRequest(r *relationRequest, err error, notify **relationRequest) {
	err = r.finishTimeout(err)
	r.err = err
	r.terminal = true
	if (err != nil || r.ref.completion != nil) && r.entry.holders[r.ticket].Mode == 0 {
		pending := false
		for _, other := range r.entry.pending {
			if other != r && !other.terminal && other.ticket == r.ticket {
				pending = true
				break
			}
		}
		if !pending {
			delete(r.ticket.resources, r.ref)
		}
	}
	if r.ticket.persistent {
		if source := r.ticket.manager.owners[r.provenance]; source != nil {
			pending := false
			for _, other := range r.entry.pending {
				if other != r && !other.terminal && other.provenance == r.provenance {
					pending = true
					break
				}
			}
			if !pending && r.entry.holders[source].Mode == 0 {
				delete(source.resources, r.ref)
			}
		}
	}
	if r.stop != nil {
		r.stop()
	}
	if r.timer != nil {
		r.timer.Stop()
	}
	r.notifyNext = *notify
	*notify = r
}
func notifyRequests(r *relationRequest) {
	for r != nil {
		next := r.notifyNext
		close(r.done)
		r = next
	}
}
func (m *relationLockManager) settle(e *relationEntry, notify **relationRequest) {
	remaining := e.pending[:0]
	for _, r := range e.pending {
		if r.terminal {
			continue
		}
		if !m.validRequest(r) {
			terminalRequest(r, ErrCatalogReplaced, notify)
			continue
		}
		if err := r.timeoutWaitError(time.Now()); err != nil {
			terminalRequest(r, err, notify)
			continue
		}
		blocked, participantHeld := false, false
		for t, held := range e.holders {
			if sameLockSession(t, r.ticket) {
				if (r.ref.Kind == advisoryBigintResource || r.ref.Kind == advisoryPairResource) && held.Mode != 0 {
					participantHeld = true
				}
			} else if Conflicts(r.mode, held.Mode) {
				blocked = true
			}
		}
		if !blocked && !participantHeld {
			for _, earlier := range remaining {
				if r.ref.completion == nil && (r.ref.Row == 0 || r.mode != noKeyUpdate) && !sameLockSession(earlier.ticket, r.ticket) && Conflicts(r.mode, earlier.mode) {
					blocked = true
					break
				}
			}
		}
		if blocked {
			remaining = append(remaining, r)
			continue
		}
		if err := r.timeoutWaitError(time.Now()); err != nil {
			terminalRequest(r, err, notify)
			continue
		}
		err := r.claimTimeoutTerminal(nil)
		if err == nil {
			err = m.grant(r.ticket, r.ref, e, r.mode)
		}
		terminalRequest(r, err, notify)
	}
	clear(e.pending[len(remaining):])
	e.pending = remaining
}
func (m *relationLockManager) Acquire(ctx context.Context, t *relationTicket, ref lockResource, mode relationLockMode) error {
	request, err := m.StartAcquire(ctx, t, ref, mode, relationLockPolicy{})
	if err != nil || request == nil {
		return err
	}
	return request.Wait()
}

// Wait blocks until the request is terminal and returns its outcome.
func (r *relationRequest) Wait() error { return r.wait() }

// wait does the blocking. The reference suite recognizes a parked lock wait
// by this frame's name, (*relationRequest).wait, so keep both names.
func (r *relationRequest) wait() error {
	<-r.done
	return r.err
}

// Done is closed once the request is terminal; Err is valid after that.
func (r *relationRequest) Done() <-chan struct{} { return r.done }

// Err is the terminal outcome. Read it only after Done is closed.
func (r *relationRequest) Err() error { return r.err }

// StartAcquire grants immediately or returns the exact journaled request.
// The caller can relinquish replacement admission before waiting, without
// allowing replacement to interrupt an uncontended implicit statement.
func (m *relationLockManager) StartAcquire(ctx context.Context, t *relationTicket, ref lockResource, mode relationLockMode, policy relationLockPolicy) (*relationRequest, error) {
	if err := WaitError(ctx, policy, time.Time{}, time.Now()); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if !m.valid(t) {
		m.mu.Unlock()
		return nil, ErrCatalogReplaced
	}
	advisory := ref.Kind == advisoryBigintResource || ref.Kind == advisoryPairResource
	if advisory {
		if ref.ObjectRef != (ids.ObjectRef{}) || ref.Row != 0 || ref.key != nil || ref.completion != nil ||
			mode != advisoryShared && mode != advisoryExclusive || t.detached {
			m.mu.Unlock()
			return nil, errors.New("stuntgres: invalid advisory lock request")
		}
		if t.persistent {
			source := m.owners[policy.Provenance]
			if source == nil || source.detached || source.session != t.session || policy.Provenance.Epoch() < m.epoch {
				m.mu.Unlock()
				return nil, ErrCatalogReplaced
			}
		}
	} else if t.persistent || ref.Kind != ordinaryResource || ref.High != 0 || ref.Low != 0 {
		m.mu.Unlock()
		return nil, errors.New("stuntgres: invalid ordinary lock resource")
	}
	if advisory {
		// Advisory tags have no catalog ObjectRef, for either lifetime.
	} else if ref.completion != nil {
		owner := ref.completion
		if mode != transactionCompletion || owner == t || owner.manager != m || owner.ref.Epoch() != t.ref.Epoch() {
			m.mu.Unlock()
			return nil, errors.New("stuntgres: invalid transaction completion request")
		}
		if !m.valid(owner) {
			m.mu.Unlock()
			return nil, nil
		}
	} else if ref.Address.ClassID == ids.TypeClassID {
		if ref.Domain() != m.domain || ref.Epoch != t.ref.Epoch() || ref.Address.SubID != 0 || ref.Address.ObjectID == 0 || ref.Row != 0 || ref.key != nil || mode != typeExclusive {
			m.mu.Unlock()
			return nil, errors.New("stuntgres: invalid type lock request")
		}
	} else if ref.Domain() != m.domain || ref.Epoch != t.ref.Epoch() || ref.Address.ClassID != ids.RelationClassID || ref.Address.SubID != 0 || ref.Address.ObjectID == 0 || (ref.key != nil && (ref.Row != 0 || mode != uniqueClaim || ref.key.bucket.ObjectRef != ref.ObjectRef)) || (ref.key == nil && ((ref.Row != 0 && mode != rowUpdate && mode != noKeyUpdate && mode != keyShare && mode != rowShare) || (ref.Row == 0 && mode != accessShare && mode != rowExclusive && mode != accessExclusive && mode != shareUpdateExclusive && mode != shareRowExclusive && mode != relationRowShare && mode != relationShare && mode != relationExclusive))) {
		m.mu.Unlock()
		return nil, errors.New("stuntgres: invalid relation lock request")
	}
	e := m.entries[ref]
	if e == nil {
		e = &relationEntry{holders: make(map[*relationTicket]relationHold)}
		if ref.completion != nil {
			e.holders[ref.completion] = relationHold{Mode: transactionCompletion}
			ref.completion.resources[ref] = struct{}{}
		}
		m.entries[ref] = e
	}
	if e.holders[t].Mode&mode != 0 {
		err := WaitError(ctx, policy, time.Time{}, time.Now())
		if err == nil {
			err = m.grant(t, ref, e, mode)
		}
		m.mu.Unlock()
		return nil, err
	}
	eligible, participantHeld := true, false
	for other, held := range e.holders {
		if advisory && sameLockSession(other, t) && held.Mode != 0 {
			participantHeld = true
		}
		if !sameLockSession(other, t) && Conflicts(mode, held.Mode) {
			eligible = false
		}
	}
	// Self reentry bypasses requests blocked by this owner's existing hold.
	// A compatible tuple NO KEY UPDATE also need not wait behind a pending
	// UPDATE blocked by KEY SHARE; actual granted tuple modes still conflict.
	if eligible && e.holders[t].Mode == 0 && !participantHeld {
		for _, earlier := range e.pending {
			if ref.completion == nil && (ref.Row == 0 || mode != noKeyUpdate) && !earlier.terminal && !sameLockSession(earlier.ticket, t) && Conflicts(mode, earlier.mode) {
				eligible = false
				break
			}
		}
	}
	if eligible {
		err := WaitError(ctx, policy, time.Time{}, time.Now())
		if err == nil {
			err = m.grant(t, ref, e, mode)
		}
		m.retire(ref, e)
		m.mu.Unlock()
		return nil, err
	}
	if err := WaitError(ctx, policy, time.Time{}, time.Now()); err != nil {
		m.retire(ref, e)
		m.mu.Unlock()
		return nil, err
	}
	if policy.NoWait {
		m.retire(ref, e)
		m.mu.Unlock()
		return nil, errRelationUnavailable
	}
	if t.resources == nil {
		t.resources = make(map[lockResource]struct{})
	}
	t.resources[ref] = struct{}{}
	provenance := t.ref
	if t.persistent {
		provenance = policy.Provenance
		m.owners[provenance].resources[ref] = struct{}{}
	}
	r := &relationRequest{ticket: t, provenance: provenance, ctx: ctx, mode: mode, done: make(chan struct{}), policy: policy, ref: ref, entry: e}
	if policy.Timeout > 0 {
		r.deadline = time.Now().Add(policy.Timeout)
	}
	if r.tracksTimeout() {
		policy.TimeoutSlot.request.Store(r)
	}
	interrupt := func() {
		var notify *relationRequest
		m.mu.Lock()
		if !r.terminal {
			if err := r.timeoutWaitError(time.Now()); err != nil {
				terminalRequest(r, err, &notify)
			}
			m.settle(e, &notify)
			m.retire(ref, e)
		}
		m.mu.Unlock()
		notifyRequests(notify)
	}
	r.stop = context.AfterFunc(ctx, interrupt)
	if !r.deadline.IsZero() {
		r.timer = time.AfterFunc(time.Until(r.deadline), interrupt)
	}
	var notify *relationRequest
	r.enqueuedAt = time.Now().UTC().Truncate(time.Microsecond)
	e.pending = append(e.pending, r)
	m.settle(e, &notify)
	if !r.terminal {
		m.detectDeadlock(r, &notify)
	}
	m.retire(ref, e)
	m.mu.Unlock()
	notifyRequests(notify)
	return r, nil
}
func (m *relationLockManager) retire(ref lockResource, e *relationEntry) {
	if len(e.holders) == 0 && len(e.pending) == 0 && e.pins == 0 {
		delete(m.entries, ref)
		if ref.key != nil {
			m.retireUniqueKey(ref.key)
		}
	}
}

// Transaction tickets end through Engine.endLockOwner, which first retires
// engine-held exclusion state; only session tickets end here directly.
func (m *relationLockManager) End(t *relationTicket) {
	if t == nil {
		return
	}
	var notify *relationRequest
	m.mu.Lock()
	m.endLocked(t, errRelationOwnerEnded, &notify)
	m.mu.Unlock()
	notifyRequests(notify)
}
func (m *relationLockManager) endLocked(t *relationTicket, err error, notify **relationRequest) {
	if !m.valid(t) {
		return
	}
	t.retireOwnership()
	if t.persistent {
		delete(m.sessions, t.session)
	} else {
		delete(m.owners, t.ref)
	}
	for ref := range t.resources {
		e := m.entries[ref]
		if e == nil {
			continue
		}
		delete(e.holders, t)
		for _, r := range e.pending {
			if (r.ticket == t || !t.persistent && r.provenance == t.ref) && !r.terminal {
				terminalRequest(r, err, notify)
			}
		}
		m.settle(e, notify)
		m.retire(ref, e)
	}
	clear(t.resources)
	m.retireMultiOwnerLocked(t)
	t.multiContributions = nil
	t.multiCache = nil
}
func (m *relationLockManager) Fence(epoch ids.CatalogEpoch) {
	var notify *relationRequest
	m.mu.Lock()
	if epoch > m.epoch {
		m.retireMultiXactsLocked()
		m.epoch = epoch
		for ref := range m.owners {
			if ref.Epoch() < epoch {
				m.owners[ref].retireOwnership()
				clear(m.owners[ref].resources)
				delete(m.owners, ref)
			}
		}
		for ref, e := range m.entries {
			if ref.Kind != ordinaryResource {
				for ticket := range e.holders {
					if !ticket.persistent && ticket.ref.Epoch() < epoch {
						delete(e.holders, ticket)
					}
				}
				for _, r := range e.pending {
					if !r.terminal && r.provenance.Epoch() < epoch {
						terminalRequest(r, ErrCatalogReplaced, &notify)
					}
				}
				m.settle(e, &notify)
				m.retire(ref, e)
				continue
			}
			if ref.Epoch < epoch && (ref.completion == nil || ref.completion.ref.Epoch() < epoch) {
				for _, r := range e.pending {
					if !r.terminal {
						terminalRequest(r, ErrCatalogReplaced, &notify)
					}
				}
				delete(m.entries, ref)
				if ref.key != nil {
					m.retireUniqueKey(ref.key)
				}
			}
		}
	}
	m.mu.Unlock()
	notifyRequests(notify)
}

type completionLockPurpose uint8

const (
	completionVirtual completionLockPurpose = iota
	completionTransaction
)
