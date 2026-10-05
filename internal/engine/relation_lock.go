package engine

import (
	"context"
	"errors"
	"sync"
)

var errRelationOwnerEnded = errors.New("stuntgres: transaction ended")

type relationLockMode uint8

const (
	accessShare relationLockMode = 1 << iota
	rowExclusive
	accessExclusive
)

type relationTicket struct {
	manager   *relationLockManager
	ref       TxRef
	resources map[ObjectRef]struct{}
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
	entries map[ObjectRef]*relationEntry
}

func newRelationLockManager(e *Engine) *relationLockManager {
	return &relationLockManager{engine: e, catalog: e.owner, owners: make(map[TxRef]*relationTicket), entries: make(map[ObjectRef]*relationEntry)}
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
	t := &relationTicket{manager: m, ref: ref, resources: make(map[ObjectRef]struct{})}
	m.owners[ref] = t
	return t, nil
}
func lockConflict(a, b relationLockMode) bool {
	return a&accessExclusive != 0 || b&accessExclusive != 0
}
func (m *relationLockManager) valid(t *relationTicket) bool {
	return t != nil && t.manager == m && m.owners[t.ref] == t
}
func terminalRequest(r *relationRequest, err error, notify **relationRequest) {
	r.err = err
	r.terminal = true
	if r.stop != nil {
		r.stop()
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
		if err := r.ctx.Err(); err != nil {
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
		if err := r.ctx.Err(); err != nil {
			terminalRequest(r, err, notify)
			continue
		}
		e.holders[r.ticket] |= r.mode
		terminalRequest(r, nil, notify)
	}
	clear(e.pending[len(remaining):])
	e.pending = remaining
}
func (m *relationLockManager) acquire(ctx context.Context, t *relationTicket, ref ObjectRef, mode relationLockMode) error {
	request, err := m.startAcquire(ctx, t, ref, mode)
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
func (m *relationLockManager) startAcquire(ctx context.Context, t *relationTicket, ref ObjectRef, mode relationLockMode) (*relationRequest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if !m.valid(t) {
		m.mu.Unlock()
		return nil, ErrCatalogReplaced
	}
	if ref.owner != m.catalog || ref.Epoch != t.ref.epoch || ref.Address.ClassID != RelationClassID || ref.Address.SubID != 0 || ref.Address.ObjectID == 0 || (mode != accessShare && mode != rowExclusive && mode != accessExclusive) {
		m.mu.Unlock()
		return nil, errors.New("stuntgres: invalid relation lock request")
	}
	t.resources[ref] = struct{}{}
	e := m.entries[ref]
	if e == nil {
		e = &relationEntry{holders: make(map[*relationTicket]relationLockMode)}
		m.entries[ref] = e
	}
	if e.holders[t]&mode != 0 {
		m.mu.Unlock()
		return nil, ctx.Err()
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
		err := ctx.Err()
		if err == nil {
			e.holders[t] |= mode
		}
		m.retire(ref, e)
		m.mu.Unlock()
		return nil, err
	}
	r := &relationRequest{ticket: t, ctx: ctx, mode: mode, done: make(chan struct{})}
	r.stop = context.AfterFunc(ctx, func() {
		var notify *relationRequest
		m.mu.Lock()
		if !r.terminal {
			terminalRequest(r, ctx.Err(), &notify)
			m.settle(e, &notify)
			m.retire(ref, e)
		}
		m.mu.Unlock()
		notifyRequests(notify)
	})
	var notify *relationRequest
	if err := ctx.Err(); err != nil {
		terminalRequest(r, err, &notify)
	} else {
		e.pending = append(e.pending, r)
		m.settle(e, &notify)
	}
	m.retire(ref, e)
	m.mu.Unlock()
	notifyRequests(notify)
	return r, nil
}
func (m *relationLockManager) retire(ref ObjectRef, e *relationEntry) {
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
}
func (m *relationLockManager) fence(epoch CatalogEpoch) {
	var notify *relationRequest
	m.mu.Lock()
	if epoch > m.epoch {
		m.epoch = epoch
		for ref := range m.owners {
			if ref.epoch < epoch {
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
