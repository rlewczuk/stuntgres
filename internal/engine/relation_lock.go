package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"stuntgres/internal/sqlstate"
)

var errRelationOwnerEnded = errors.New("stuntgres: transaction ended")

var errRelationUnavailable = errors.New("stuntgres: relation lock unavailable")

type relationLockPolicy struct {
	nowait            bool
	timeout           time.Duration
	statementDeadline time.Time
	source            context.Context
	provenance        TxRef                 // current statement owner for a persistent session request
	operation         *Operation            // exact admitted command, atomic timeout projection only
	completionPurpose completionLockPurpose // observational identity; never changes arbitration
}

type relationSourceError struct{ error }

func (e relationSourceError) Unwrap() error { return e.error }

// Absolute SQL deadlines, not callback scheduling, determine a wait's error.
func relationWaitError(ctx context.Context, policy relationLockPolicy, lockDeadline, now time.Time) error {
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
	ObjectRef
	kind       lockResourceKind
	high, low  uint32
	row        RowID
	key        *uniqueLockKey
	completion *relationTicket
}
type relationTicket struct {
	manager           *relationLockManager
	ref               TxRef
	session           SessionToken
	persistent        bool
	detached          bool // prepared transaction: independent of its former physical session
	resources         map[lockResource]struct{}
	assignedXID       uint32 // sync/atomic only: canonical TX mirror; end or completed index phase retires ownership
	uniqueDeltaActive bool
	uniqueDelta       []lockResource
}

func (t *relationTicket) assignedPublicXID() uint32    { return atomic.LoadUint32(&t.assignedXID) }
func (t *relationTicket) mirrorAssignedXID(xid uint32) { atomic.StoreUint32(&t.assignedXID, xid) }

type relationRequest struct {
	ticket       *relationTicket
	provenance   TxRef
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
	mode              relationLockMode
	shared, exclusive uint64
}

type relationEntry struct {
	holders map[*relationTicket]relationHold
	pending []*relationRequest
	pins    int
}
type relationLockManager struct {
	mu         sync.Mutex
	engine     *Engine
	catalog    *catalogOwner
	epoch      CatalogEpoch
	owners     map[TxRef]*relationTicket
	sessions   map[SessionToken]*relationTicket
	entries    map[lockResource]*relationEntry
	keyBuckets map[uniqueLockBucket]*uniqueLockBucketState
}

func newRelationLockManager(e *Engine) *relationLockManager {
	return &relationLockManager{engine: e, catalog: e.owner, owners: make(map[TxRef]*relationTicket), entries: make(map[lockResource]*relationEntry)}
}
func (m *relationLockManager) register(ref TxRef) (*relationTicket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ref.owner != m.engine || ref.id == 0 || ref.epoch == 0 || ref.attachment == nil || ref.session.owner != m.engine ||
		ref.session.id == 0 && (ref.session.private == nil || ref.attachment.session != ref.session.private || ref.session.private.token != ref.session) ||
		ref.session.id != 0 && ref.session.private != nil {
		return nil, errors.New("stuntgres: invalid lock owner")
	}
	if ref.epoch < m.epoch {
		return nil, ErrCatalogReplaced
	}
	if _, ok := m.owners[ref]; ok {
		return nil, errors.New("stuntgres: duplicate lock owner")
	}
	t := &relationTicket{manager: m, ref: ref, session: ref.session, resources: make(map[lockResource]struct{})}
	m.owners[ref] = t
	return t, nil
}
func lockConflict(a, b relationLockMode) bool {
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
type relationLockCheckpoint map[lockResource]relationLockMode

func (m *relationLockManager) checkpoint(t *relationTicket) relationLockCheckpoint {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.valid(t) || t.persistent {
		return nil
	}
	held := make(relationLockCheckpoint, len(t.resources))
	for ref := range t.resources {
		if ref.completion != nil || ref.kind != ordinaryResource {
			continue
		}
		if entry := m.entries[ref]; entry != nil && entry.holders[t].mode != 0 {
			held[ref] = entry.holders[t].mode
		}
	}
	return held
}

// Caller serializes the owner but holds no catalog/session/publication locks.
// Intersecting prevents a stale checkpoint from resurrecting ownership.
func (m *relationLockManager) restore(t *relationTicket, held relationLockCheckpoint) {
	var notify *relationRequest
	m.mu.Lock()
	if m.valid(t) && !t.persistent {
		for ref := range t.resources {
			if ref.completion != nil || ref.kind != ordinaryResource {
				continue
			}
			entry := m.entries[ref]
			if entry == nil {
				continue
			}
			mode := entry.holders[t].mode & held[ref]
			if mode == entry.holders[t].mode {
				continue
			}
			if mode == 0 {
				delete(entry.holders, t)
				delete(t.resources, ref)
			} else {
				entry.holders[t] = relationHold{mode: mode}
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
func (m *relationLockManager) checkpointUnique(t *relationTicket, held relationLockCheckpoint) relationLockCheckpoint {
	m.mu.Lock()
	defer m.mu.Unlock()
	if held == nil {
		held = make(relationLockCheckpoint)
	}
	clear(held)
	if m.valid(t) && !t.persistent {
		for ref := range t.resources {
			if ref.key != nil && ref.kind == ordinaryResource && ref.completion == nil {
				if entry := m.entries[ref]; entry != nil && entry.holders[t].mode != 0 {
					held[ref] = entry.holders[t].mode
				}
			}
		}
	}
	return held
}
func (m *relationLockManager) restoreUnique(t *relationTicket, held relationLockCheckpoint) {
	var notify *relationRequest
	m.mu.Lock()
	if m.valid(t) && !t.persistent {
		for ref := range t.resources {
			if ref.key == nil || ref.kind != ordinaryResource || ref.completion != nil {
				continue
			}
			entry := m.entries[ref]
			if entry == nil {
				continue
			}
			hold := entry.holders[t]
			mode := hold.mode & held[ref]
			if mode == hold.mode {
				continue
			}
			if mode == 0 {
				delete(entry.holders, t)
				delete(t.resources, ref)
			} else {
				hold.mode = mode
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
	if (err != nil || r.ref.completion != nil) && r.entry.holders[r.ticket].mode == 0 {
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
			if !pending {
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
		blocked := false
		for t, held := range e.holders {
			if !sameLockSession(t, r.ticket) && lockConflict(r.mode, held.mode) {
				blocked = true
				break
			}
		}
		if !blocked {
			for _, earlier := range remaining {
				if r.ref.completion == nil && (r.ref.row == 0 || r.mode != noKeyUpdate) && !sameLockSession(earlier.ticket, r.ticket) && lockConflict(r.mode, earlier.mode) {
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
func (m *relationLockManager) acquire(ctx context.Context, t *relationTicket, ref lockResource, mode relationLockMode) error {
	request, err := m.startAcquire(ctx, t, ref, mode, relationLockPolicy{})
	if err != nil || request == nil {
		return err
	}
	return request.wait()
}

func (r *relationRequest) wait() error {
	<-r.done
	return r.err
}

// startAcquire grants immediately or returns the exact journaled request.
// The caller can relinquish replacement admission before waiting, without
// allowing replacement to interrupt an uncontended implicit statement.
func (m *relationLockManager) startAcquire(ctx context.Context, t *relationTicket, ref lockResource, mode relationLockMode, policy relationLockPolicy) (*relationRequest, error) {
	if err := relationWaitError(ctx, policy, time.Time{}, time.Now()); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if !m.valid(t) {
		m.mu.Unlock()
		return nil, ErrCatalogReplaced
	}
	if t.persistent {
		if ref.kind != advisoryBigintResource && ref.kind != advisoryPairResource ||
			ref.ObjectRef != (ObjectRef{}) || ref.row != 0 || ref.key != nil || ref.completion != nil ||
			mode != advisoryShared && mode != advisoryExclusive || policy.provenance.session != t.session {
			m.mu.Unlock()
			return nil, errors.New("stuntgres: invalid advisory lock request")
		}
		source := m.owners[policy.provenance]
		if source == nil || source.detached || policy.provenance.epoch < m.epoch {
			m.mu.Unlock()
			return nil, ErrCatalogReplaced
		}
	} else if ref.kind != ordinaryResource || ref.high != 0 || ref.low != 0 {
		m.mu.Unlock()
		return nil, errors.New("stuntgres: invalid ordinary lock resource")
	}
	if t.persistent {
		// Session resources were validated above without a fabricated catalog ref.
	} else if ref.completion != nil {
		owner := ref.completion
		if mode != transactionCompletion || owner == t || owner.manager != m || owner.ref.epoch != t.ref.epoch {
			m.mu.Unlock()
			return nil, errors.New("stuntgres: invalid transaction completion request")
		}
		if !m.valid(owner) {
			m.mu.Unlock()
			return nil, nil
		}
	} else if ref.Address.ClassID == TypeClassID {
		if ref.owner != m.catalog || ref.Epoch != t.ref.epoch || ref.Address.SubID != 0 || ref.Address.ObjectID == 0 || ref.row != 0 || ref.key != nil || mode != typeExclusive {
			m.mu.Unlock()
			return nil, errors.New("stuntgres: invalid type lock request")
		}
	} else if ref.owner != m.catalog || ref.Epoch != t.ref.epoch || ref.Address.ClassID != RelationClassID || ref.Address.SubID != 0 || ref.Address.ObjectID == 0 || (ref.key != nil && (ref.row != 0 || mode != uniqueClaim || ref.key.bucket.ObjectRef != ref.ObjectRef)) || (ref.key == nil && ((ref.row != 0 && mode != rowUpdate && mode != noKeyUpdate && mode != keyShare && mode != rowShare) || (ref.row == 0 && mode != accessShare && mode != rowExclusive && mode != accessExclusive && mode != shareUpdateExclusive && mode != shareRowExclusive && mode != relationRowShare && mode != relationShare && mode != relationExclusive))) {
		m.mu.Unlock()
		return nil, errors.New("stuntgres: invalid relation lock request")
	}
	e := m.entries[ref]
	if e == nil {
		e = &relationEntry{holders: make(map[*relationTicket]relationHold)}
		if ref.completion != nil {
			e.holders[ref.completion] = relationHold{mode: transactionCompletion}
			ref.completion.resources[ref] = struct{}{}
		}
		m.entries[ref] = e
	}
	if e.holders[t].mode&mode != 0 {
		err := relationWaitError(ctx, policy, time.Time{}, time.Now())
		if err == nil {
			err = m.grant(t, ref, e, mode)
		}
		m.mu.Unlock()
		return nil, err
	}
	eligible := true
	for other, held := range e.holders {
		if !sameLockSession(other, t) && lockConflict(mode, held.mode) {
			eligible = false
			break
		}
	}
	// Self reentry bypasses requests blocked by this owner's existing hold.
	// A compatible tuple NO KEY UPDATE also need not wait behind a pending
	// UPDATE blocked by KEY SHARE; actual granted tuple modes still conflict.
	if eligible && e.holders[t].mode == 0 {
		for _, earlier := range e.pending {
			if ref.completion == nil && (ref.row == 0 || mode != noKeyUpdate) && !earlier.terminal && !sameLockSession(earlier.ticket, t) && lockConflict(mode, earlier.mode) {
				eligible = false
				break
			}
		}
	}
	if eligible {
		err := relationWaitError(ctx, policy, time.Time{}, time.Now())
		if err == nil {
			err = m.grant(t, ref, e, mode)
		}
		m.retire(ref, e)
		m.mu.Unlock()
		return nil, err
	}
	if err := relationWaitError(ctx, policy, time.Time{}, time.Now()); err != nil {
		m.retire(ref, e)
		m.mu.Unlock()
		return nil, err
	}
	if policy.nowait {
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
		provenance = policy.provenance
		m.owners[provenance].resources[ref] = struct{}{}
	}
	r := &relationRequest{ticket: t, provenance: provenance, ctx: ctx, mode: mode, done: make(chan struct{}), policy: policy, ref: ref, entry: e}
	if policy.timeout > 0 {
		r.deadline = time.Now().Add(policy.timeout)
	}
	if r.tracksTimeout() {
		policy.operation.pendingTimeoutRequest.Store(r)
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
func (m *relationLockManager) end(t *relationTicket) {
	if t == nil {
		return
	}
	// Retire dirty visibility before releasing the existing completion resource.
	if t.ref.owner != nil {
		t.ref.owner.removeExclusionOwner(t)
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
}
func (m *relationLockManager) fence(epoch CatalogEpoch) {
	var notify *relationRequest
	m.mu.Lock()
	if epoch > m.epoch {
		m.epoch = epoch
		for ref := range m.owners {
			if ref.epoch < epoch {
				clear(m.owners[ref].resources)
				delete(m.owners, ref)
			}
		}
		for ref, e := range m.entries {
			if ref.kind != ordinaryResource {
				for _, r := range e.pending {
					if !r.terminal && r.provenance.epoch < epoch {
						terminalRequest(r, ErrCatalogReplaced, &notify)
					}
				}
				m.settle(e, &notify)
				m.retire(ref, e)
				continue
			}
			if ref.Epoch < epoch && (ref.completion == nil || ref.completion.ref.epoch < epoch) {
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
