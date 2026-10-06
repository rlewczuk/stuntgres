package engine

import (
	"context"
	"errors"
	"sync"
	"time"
)

var errRelationOwnerEnded = errors.New("stuntgres: transaction ended")

var errRelationUnavailable = errors.New("stuntgres: relation lock unavailable")

type relationLockPolicy struct {
	nowait            bool
	timeout           time.Duration
	statementDeadline time.Time
	source            context.Context
}

type relationSourceError struct{ error }

func (e relationSourceError) Unwrap() error { return e.error }

// Absolute SQL deadlines, not callback scheduling, determine a wait's error.
func relationWaitError(ctx context.Context, policy relationLockPolicy, lockDeadline, now time.Time) error {
	if policy.source != nil {
		if errors.Is(policy.source.Err(), context.Canceled) {
			return relationSourceError{context.Canceled}
		}
		if deadline, ok := policy.source.Deadline(); ok && !now.Before(deadline) &&
			(lockDeadline.IsZero() || !lockDeadline.Before(deadline)) &&
			(policy.statementDeadline.IsZero() || !policy.statementDeadline.Before(deadline)) {
			return relationSourceError{context.DeadlineExceeded}
		}
	}
	lockExpired := !lockDeadline.IsZero() && !now.Before(lockDeadline)
	statementExpired := !policy.statementDeadline.IsZero() && !now.Before(policy.statementDeadline)
	if lockExpired && (!statementExpired || !policy.statementDeadline.Before(lockDeadline)) {
		return sqlError("55P03", "canceling statement due to lock timeout")
	}
	if statementExpired {
		return sqlError("57014", "canceling statement due to statement timeout")
	}
	return ctx.Err()
}

type relationLockMode uint8

const (
	accessShare relationLockMode = 1 << iota
	rowExclusive
	accessExclusive
	shareUpdateExclusive
	rowUpdate
)

// Row zero is the relation resource; logical rows use their stable RowID.
type lockResource struct {
	ObjectRef
	row RowID
}

type relationTicket struct {
	manager   *relationLockManager
	ref       TxRef
	resources map[lockResource]struct{}
}
type relationRequest struct {
	ticket     *relationTicket
	ctx        context.Context
	mode       relationLockMode
	done       chan struct{}
	err        error
	terminal   bool
	notifyNext *relationRequest
	stop       func() bool
	timer      *time.Timer
	policy     relationLockPolicy
	deadline   time.Time
	ref        lockResource
	entry      *relationEntry
}
type relationEntry struct {
	holders map[*relationTicket]relationLockMode
	pending []*relationRequest
}
type relationLockManager struct {
	mu      sync.Mutex
	engine  *Engine
	catalog *catalogOwner
	epoch   CatalogEpoch
	owners  map[TxRef]*relationTicket
	entries map[lockResource]*relationEntry
}

func newRelationLockManager(e *Engine) *relationLockManager {
	return &relationLockManager{engine: e, catalog: e.owner, owners: make(map[TxRef]*relationTicket), entries: make(map[lockResource]*relationEntry)}
}
func (m *relationLockManager) register(ref TxRef) (*relationTicket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ref.owner != m.engine || ref.id == 0 || ref.epoch == 0 || ref.attachment == nil || ref.session.owner != m.engine || ref.session.id == 0 {
		return nil, errors.New("stuntgres: invalid lock owner")
	}
	if ref.epoch < m.epoch {
		return nil, ErrCatalogReplaced
	}
	if _, ok := m.owners[ref]; ok {
		return nil, errors.New("stuntgres: duplicate lock owner")
	}
	t := &relationTicket{manager: m, ref: ref, resources: make(map[lockResource]struct{})}
	m.owners[ref] = t
	return t, nil
}
func lockConflict(a, b relationLockMode) bool {
	return a&rowUpdate != 0 && b&rowUpdate != 0 || a&accessExclusive != 0 || b&accessExclusive != 0 || a&shareUpdateExclusive != 0 && b&shareUpdateExclusive != 0
}
func (m *relationLockManager) valid(t *relationTicket) bool {
	return t != nil && t.manager == m && m.owners[t.ref] == t
}
func terminalRequest(r *relationRequest, err error, notify **relationRequest) {
	r.err = err
	r.terminal = true
	if err != nil && r.entry.holders[r.ticket] == 0 {
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
		if err := relationWaitError(r.ctx, r.policy, r.deadline, time.Now()); err != nil {
			terminalRequest(r, err, notify)
			continue
		}
		blocked := false
		for t, mode := range e.holders {
			if t != r.ticket && lockConflict(r.mode, mode) {
				blocked = true
				break
			}
		}
		if !blocked {
			for _, earlier := range remaining {
				if earlier.ticket != r.ticket && lockConflict(r.mode, earlier.mode) {
					blocked = true
					break
				}
			}
		}
		if blocked {
			remaining = append(remaining, r)
			continue
		}
		if err := relationWaitError(r.ctx, r.policy, r.deadline, time.Now()); err != nil {
			terminalRequest(r, err, notify)
			continue
		}
		e.holders[r.ticket] |= r.mode
		terminalRequest(r, nil, notify)
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
	if ref.owner != m.catalog || ref.Epoch != t.ref.epoch || ref.Address.ClassID != RelationClassID || ref.Address.SubID != 0 || ref.Address.ObjectID == 0 || (ref.row != 0 && mode != rowUpdate) || (ref.row == 0 && mode != accessShare && mode != rowExclusive && mode != accessExclusive && mode != shareUpdateExclusive) {
		m.mu.Unlock()
		return nil, errors.New("stuntgres: invalid relation lock request")
	}
	e := m.entries[ref]
	if e == nil {
		e = &relationEntry{holders: make(map[*relationTicket]relationLockMode)}
		m.entries[ref] = e
	}
	if e.holders[t]&mode != 0 {
		t.resources[ref] = struct{}{}
		m.mu.Unlock()
		return nil, relationWaitError(ctx, policy, time.Time{}, time.Now())
	}
	eligible := true
	for other, held := range e.holders {
		if other != t && lockConflict(mode, held) {
			eligible = false
			break
		}
	}
	// Self reentry bypasses requests blocked by this owner's existing hold.
	if eligible && e.holders[t] == 0 {
		for _, earlier := range e.pending {
			if !earlier.terminal && earlier.ticket != t && lockConflict(mode, earlier.mode) {
				eligible = false
				break
			}
		}
	}
	if eligible {
		err := relationWaitError(ctx, policy, time.Time{}, time.Now())
		if err == nil {
			e.holders[t] |= mode
			t.resources[ref] = struct{}{}
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
	t.resources[ref] = struct{}{}
	r := &relationRequest{ticket: t, ctx: ctx, mode: mode, done: make(chan struct{}), policy: policy, ref: ref, entry: e}
	if policy.timeout > 0 {
		r.deadline = time.Now().Add(policy.timeout)
	}
	interrupt := func() {
		var notify *relationRequest
		m.mu.Lock()
		if !r.terminal {
			if err := relationWaitError(ctx, policy, r.deadline, time.Now()); err != nil {
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
	e.pending = append(e.pending, r)
	m.settle(e, &notify)
	m.retire(ref, e)
	m.mu.Unlock()
	notifyRequests(notify)
	return r, nil
}
func (m *relationLockManager) retire(ref lockResource, e *relationEntry) {
	if len(e.holders) == 0 && len(e.pending) == 0 {
		delete(m.entries, ref)
	}
}
func (m *relationLockManager) end(t *relationTicket) {
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
	delete(m.owners, t.ref)
	for ref := range t.resources {
		e := m.entries[ref]
		if e == nil {
			continue
		}
		delete(e.holders, t)
		for _, r := range e.pending {
			if r.ticket == t && !r.terminal {
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
			if ref.Epoch < epoch {
				for _, r := range e.pending {
					if !r.terminal {
						terminalRequest(r, ErrCatalogReplaced, &notify)
					}
				}
				delete(m.entries, ref)
			}
		}
	}
	m.mu.Unlock()
	notifyRequests(notify)
}
