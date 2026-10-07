package engine

import (
	"errors"
	"sync/atomic"
)

// VisibilityView retains exact immutable relation-local roots, not copied row
// values. Each handle has independent release authority. The immutable data is
// naturally reclaimed after the last view/result owner; no registry is needed.
type visibilityData struct {
	tx        TxRef
	command   CommandID
	frontier  CommitSeq
	catalog   CatalogRead
	committed map[ObjectAddress][]rowVersion
	storage   *relationStorage // Exact shape generation of this one-relation view.
	own       map[ObjectAddress][]rowVersion
	ends      map[ObjectAddress]*deletionRoot
	deleted   map[ObjectAddress]*privateDeletionRoot
}

type VisibilityView struct {
	self *VisibilityView
	data atomic.Pointer[visibilityData]
}

func newVisibilityView(data *visibilityData) *VisibilityView {
	v := &VisibilityView{}
	v.self = v
	v.data.Store(data)
	return v
}

func (s *StatementScope) AcquireDataView(ref ObjectRef) (*VisibilityView, error) {
	if err := s.active(); err != nil {
		return nil, err
	}
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	if !s.worked || s.owner.state == TxIdle || s.owner.state == TxAborted {
		return nil, errors.New("stuntgres: inactive data view scope")
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	return s.acquireDataViewLocked(e, ref)
}

func (s *StatementScope) acquireDataViewLocked(e *Engine, ref ObjectRef) (*VisibilityView, error) {
	if ref.owner != e.owner {
		return nil, errors.New("stuntgres: foreign catalog reference")
	}
	if ref.Epoch != e.state.epoch || s.txRef.epoch != e.state.epoch {
		return nil, ErrCatalogReplaced
	}
	storage := e.storage[ref.Address]
	if storage == nil {
		return nil, errors.New("stuntgres: relation storage is missing")
	}
	if s.view != nil {
		if s.view.data.Load() == nil {
			return nil, errors.New("stuntgres: visibility view is released")
		}
		if _, ok := s.view.read().committed[ref.Address]; !ok {
			return nil, errors.New("stuntgres: relation is outside statement data view")
		}
		return s.view, nil
	}
	if s.txRef.epoch != e.state.epoch {
		return nil, ErrCatalogReplaced
	}
	versions, ends, frontier := storage.versions, storage.ends, e.commitSeq
	if s.owner.isolation == RepeatableRead {
		frontier = s.owner.frontier
		root, exists := s.owner.roots[ref.Address]
		if exists {
			current, ok := e.state.descriptors[ref.Address].(*relationDescriptor)
			if !ok || root.storage != storage || !sameRelationRowShape(root.relation, current) {
				return nil, ErrCatalogReplaced
			}
			versions, ends = root.versions, root.ends
		} else {
			versions, ends = nil, nil
		}
	}
	data := &visibilityData{
		tx: s.txRef, command: s.command, frontier: frontier,
		catalog:   CatalogRead{owner: e, state: e.state},
		committed: map[ObjectAddress][]rowVersion{ref.Address: versions},
		storage:   storage,
	}
	if ends != nil {
		data.ends = map[ObjectAddress]*deletionRoot{ref.Address: ends}
	}
	data.deleted = s.ownDeletes
	if rows := s.ownWrites[ref.Address]; len(rows) != 0 {
		data.own = map[ObjectAddress][]rowVersion{ref.Address: rows}
	}
	s.view = newVisibilityView(data)
	return s.view, nil
}

func (v *VisibilityView) Retain() *VisibilityView {
	if v == nil || v.self != v {
		return nil
	}
	data := v.data.Load()
	if data == nil {
		return nil
	}
	return newVisibilityView(data)
}

func (v *VisibilityView) Release() {
	if v != nil && v.self == v {
		v.data.Store(nil)
	}
}

func (v *VisibilityView) read() *visibilityData {
	if v == nil || v.self != v {
		panic("stuntgres: visibility view is released")
	}
	data := v.data.Load()
	if data == nil {
		panic("stuntgres: visibility view is released")
	}
	return data
}

func (v *VisibilityView) Tx() TxRef            { return v.read().tx }
func (v *VisibilityView) Command() CommandID   { return v.read().command }
func (v *VisibilityView) Frontier() CommitSeq  { return v.read().frontier }
func (v *VisibilityView) Catalog() CatalogRead { return v.read().catalog }
