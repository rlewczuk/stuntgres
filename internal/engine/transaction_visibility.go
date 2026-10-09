package engine

import (
	"errors"
	"sync/atomic"
)

// VisibilityView retains exact immutable relation-local roots, not copied row
// values. Each handle has independent release authority. The immutable data is
// naturally reclaimed after the last view/result owner; no registry is needed.
type visibilityData struct {
	tx                 TxRef
	command            CommandID
	frontier           CommitSeq
	rewrittenFrontiers map[ObjectAddress]CommitSeq
	catalog            CatalogRead
	committed          map[ObjectAddress][]rowVersion
	generations        map[ObjectAddress]*relationStorage
	singleGeneration   *relationStorage
	own                map[ObjectAddress][]rowVersion
	ends               map[ObjectAddress]*deletionRoot
	deleted            map[ObjectAddress]*privateDeletionRoot
}

func (d *visibilityData) generationAt(address ObjectAddress) *relationStorage {
	if d.generations != nil {
		return d.generations[address]
	}
	if _, ok := d.committed[address]; ok {
		return d.singleGeneration
	}
	return nil
}

func (d *visibilityData) frontierAt(address ObjectAddress) CommitSeq {
	if frontier, ok := d.rewrittenFrontiers[address]; ok {
		return frontier
	}
	return d.frontier
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
	return s.acquireDataViewsLocked(e, []ObjectRef{ref})
}

func (s *StatementScope) acquireDataViewsLocked(e *Engine, refs []ObjectRef) (*VisibilityView, error) {
	if s.txRef.epoch != e.state.epoch {
		return nil, ErrCatalogReplaced
	}
	for _, ref := range refs {
		if ref.owner != e.owner {
			return nil, errors.New("stuntgres: foreign catalog reference")
		}
		if ref.Epoch != e.state.epoch {
			return nil, ErrCatalogReplaced
		}
		if e.privateStorageLocked(s.owner.catalog, ref.Address) == nil {
			return nil, errors.New("stuntgres: relation storage is missing")
		}
	}
	if s.view != nil {
		data := s.view.data.Load()
		if data == nil {
			return nil, errors.New("stuntgres: visibility view is released")
		}
		for _, ref := range refs {
			if _, ok := data.committed[ref.Address]; !ok {
				return nil, errors.New("stuntgres: relation is outside statement data view")
			}
			if data.generationAt(ref.Address) != e.privateStorageLocked(s.owner.catalog, ref.Address) {
				return nil, ErrCatalogReplaced
			}
		}
		for _, ref := range refs {
			if err := e.serializableReadLocked(s, ref); err != nil {
				return nil, err
			}
		}
		return s.view, nil
	}
	frontier := e.commitSeq
	if stableIsolation(s.owner.isolation) {
		frontier = s.owner.frontier
	}
	data := &visibilityData{
		tx: s.txRef, command: s.command, frontier: frontier,
		catalog:   e.privateCatalogLocked(s.owner.catalog),
		committed: make(map[ObjectAddress][]rowVersion, len(refs)),
	}
	if len(refs) > 1 {
		data.generations = make(map[ObjectAddress]*relationStorage, len(refs))
	}
	for _, ref := range refs {
		storage := e.privateStorageLocked(s.owner.catalog, ref.Address)
		versions, ends := storage.versions, storage.ends
		var rewrite catalogStorageChange
		if s.owner.catalog != nil {
			rewrite = s.owner.catalog.storage[ref.Address]
		}
		if stableIsolation(s.owner.isolation) && !rewrite.rewritten {
			root, exists := s.owner.roots[ref.Address]
			if exists {
				current, ok := e.state.descriptors[ref.Address].(*relationDescriptor)
				if !ok || root.storage != e.storage[ref.Address] || !sameRelationRowShape(root.relation, current) {
					return nil, ErrCatalogReplaced
				}
				versions, ends = root.versions, root.ends
				if s.owner.catalog != nil {
					if shape, changed := s.owner.catalog.storage[ref.Address]; changed && shape.hasSnapshot {
						versions = shape.snapshot
					}
				}
			} else {
				versions, ends = nil, nil
			}
		}
		if rewrite.rewritten && stableIsolation(s.owner.isolation) {
			if data.rewrittenFrontiers == nil {
				data.rewrittenFrontiers = make(map[ObjectAddress]CommitSeq)
			}
			data.rewrittenFrontiers[ref.Address] = rewrite.frontier
		}
		data.committed[ref.Address] = versions
		if data.generations != nil {
			data.generations[ref.Address] = storage
		} else {
			data.singleGeneration = storage
		}
		if ends != nil {
			if data.ends == nil {
				data.ends = make(map[ObjectAddress]*deletionRoot)
			}
			data.ends[ref.Address] = ends
		}
		if rows := s.ownWrites[ref.Address]; len(rows) != 0 {
			if data.own == nil {
				data.own = make(map[ObjectAddress][]rowVersion)
			}
			data.own[ref.Address] = rows
		}
	}
	if len(s.ownDeletes) != 0 {
		selected := 0
		for _, ref := range refs {
			if s.ownDeletes[ref.Address] != nil {
				selected++
			}
		}
		if selected == len(s.ownDeletes) {
			// Reuse an already exact immutable root set, including the usual
			// single-target case, rather than allocating a redundant map.
			data.deleted = s.ownDeletes
		} else if selected != 0 {
			data.deleted = make(map[ObjectAddress]*privateDeletionRoot, selected)
			for _, ref := range refs {
				if root := s.ownDeletes[ref.Address]; root != nil {
					data.deleted[ref.Address] = root
				}
			}
		}
	}
	for _, ref := range refs {
		if err := e.serializableReadLocked(s, ref); err != nil {
			return nil, err
		}
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
