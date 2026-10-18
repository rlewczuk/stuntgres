package engine

import (
	"errors"
	"sync/atomic"

	"stuntgres/internal/engine/ids"
)

// VisibilityView retains exact immutable relation-local roots, not copied row
// values. Each handle has independent release authority. The immutable data is
// naturally reclaimed after the last view/result owner; no registry is needed.
// A committed TRUNCATE incarnation can be newer than the retained snapshot.
// This flag is capture metadata for the existing selector, not another frontier.
type visibilityGeneration struct {
	storage          *relationStorage
	needsBirthFilter bool
}

type visibilityData struct {
	tx                     ids.TxRef
	command                CommandID
	frontier               CommitSeq
	rewrittenFrontiers     map[ids.ObjectAddress]CommitSeq
	catalog                CatalogRead
	committed              map[ids.ObjectAddress][]rowVersion
	generations            map[ids.ObjectAddress]visibilityGeneration
	singleGeneration       *relationStorage
	singleNeedsBirthFilter bool
	own                    map[ids.ObjectAddress][]rowVersion
	ends                   map[ids.ObjectAddress]*deletionRoot
	deleted                map[ids.ObjectAddress]*privateDeletionRoot
}

func (d *visibilityData) generationAt(address ids.ObjectAddress) *relationStorage {
	if d.generations != nil {
		return d.generations[address].storage
	}
	if _, ok := d.committed[address]; ok {
		return d.singleGeneration
	}
	return nil
}

func (d *visibilityData) needsBirthFilterAt(address ids.ObjectAddress) bool {
	if d.generations != nil {
		return d.generations[address].needsBirthFilter
	}
	_, captured := d.committed[address]
	return captured && d.singleNeedsBirthFilter
}

func (d *visibilityData) frontierAt(address ids.ObjectAddress) CommitSeq {
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

func (s *StatementScope) AcquireDataView(ref ids.ObjectRef) (*VisibilityView, error) {
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

func (s *StatementScope) acquireDataViewLocked(e *Engine, ref ids.ObjectRef) (*VisibilityView, error) {
	return s.acquireDataViewsLocked(e, []ids.ObjectRef{ref})
}

func (s *StatementScope) acquireDataViewsLocked(e *Engine, refs []ids.ObjectRef) (*VisibilityView, error) {
	return s.captureDataViewsLocked(e, refs, false)
}

// ATTACH validates the grant-time committed frontier plus this owner's earlier
// commands. It does not change the RR/Serializable owner's ordinary snapshot.
func (s *StatementScope) acquirePartitionValidationViewsLocked(e *Engine, refs []ids.ObjectRef) (*VisibilityView, error) {
	return s.captureDataViewsLocked(e, refs, true)
}

func (s *StatementScope) captureDataViewsLocked(e *Engine, refs []ids.ObjectRef, latest bool) (*VisibilityView, error) {
	if s.txRef.Epoch() != e.state.epoch {
		return nil, ErrCatalogReplaced
	}
	for _, ref := range refs {
		if ref.Domain() != e.domain {
			return nil, errors.New("stuntgres: foreign catalog reference")
		}
		if ref.Epoch != e.state.epoch {
			return nil, ErrCatalogReplaced
		}
		if e.privateStorageLocked(s.owner.catalog, ref.Address) == nil {
			return nil, errors.New("stuntgres: relation storage is missing")
		}
	}
	existing := s.view
	if latest {
		existing = s.partitionValidationView
	}
	if existing != nil {
		data := existing.data.Load()
		if data == nil {
			return nil, errors.New("stuntgres: visibility view is released")
		}
		for _, ref := range refs {
			if _, ok := data.committed[ref.Address]; !ok {
				return nil, errors.New("stuntgres: relation is outside statement data view")
			}
			captured := data.generationAt(ref.Address)
			current := e.privateStorageLocked(s.owner.catalog, ref.Address)
			if captured != current {
				oldRelation, _ := data.catalog.state.descriptors[ref.Address].(*relationDescriptor)
				liveRelation, _ := e.privateCatalogLocked(s.owner.catalog).state.descriptors[ref.Address].(*relationDescriptor)
				if !sameMaterializedLineage(oldRelation, liveRelation, captured, current) {
					return nil, ErrCatalogReplaced
				}
			}
		}
		for _, ref := range refs {
			if err := e.serializableReadLocked(s, ref); err != nil {
				return nil, err
			}
		}
		return existing, nil
	}
	if !latest {
		if _, err := e.visibilitySnapshotLocked(s.txRef, false); err != nil {
			return nil, err
		}
	}
	frontier := e.commitSeq
	if !latest && stableIsolation(s.owner.isolation) {
		frontier = s.owner.frontier
	}
	catalog := e.privateCatalogLocked(s.owner.catalog)
	data := &visibilityData{
		tx: s.txRef, command: s.command, frontier: frontier,
		catalog:   catalog,
		committed: make(map[ids.ObjectAddress][]rowVersion, len(refs)),
	}
	if len(refs) > 1 {
		data.generations = make(map[ids.ObjectAddress]visibilityGeneration, len(refs))
	}
	for _, ref := range refs {
		storage := e.privateStorageLocked(s.owner.catalog, ref.Address)
		versions, ends := storage.versions, storage.ends
		needsBirthFilter := false
		var rewrite catalogStorageChange
		if s.owner.catalog != nil {
			rewrite = s.owner.catalog.storage[ref.Address]
		}
		if !latest && stableIsolation(s.owner.isolation) && !rewrite.rewritten {
			root, exists := s.owner.roots[ref.Address]
			if exists {
				current, ok := e.state.descriptors[ref.Address].(*relationDescriptor)
				live := e.storage[ref.Address]
				if !ok || !sameRelationRowShape(root.relation, current) {
					return nil, ErrCatalogReplaced
				}
				if root.storage == live {
					versions, ends = root.versions, root.ends
				} else if sameMaterializedLineage(root.relation, current, root.storage, live) {
					// Concurrent MV publication retains the actual RR root and
					// its end markers, not the new incarnation with a birth filter.
					versions, ends = root.versions, root.ends
				} else {
					if !sameTruncateLineage(root.storage, live) {
						return nil, ErrCatalogReplaced
					}
					needsBirthFilter = true
				}
				// Proven TRUNCATE retirement keeps the original frontier but
				// uses the current incarnation, never the retained old rows.
				if s.owner.catalog != nil {
					if shape, changed := s.owner.catalog.storage[ref.Address]; changed && shape.hasSnapshot {
						versions = shape.snapshot
						needsBirthFilter = false // Projected frozen snapshots retain their birth bound.
					}
				}
			} else {
				versions, ends = nil, nil
			}
		}
		if !latest && rewrite.rewritten && stableIsolation(s.owner.isolation) {
			if data.rewrittenFrontiers == nil {
				data.rewrittenFrontiers = make(map[ids.ObjectAddress]CommitSeq)
			}
			data.rewrittenFrontiers[ref.Address] = rewrite.frontier
		}
		data.committed[ref.Address] = versions
		if data.generations != nil {
			data.generations[ref.Address] = visibilityGeneration{storage: storage, needsBirthFilter: needsBirthFilter}
		} else {
			data.singleGeneration = storage
			data.singleNeedsBirthFilter = needsBirthFilter
		}
		if ends != nil {
			if data.ends == nil {
				data.ends = make(map[ids.ObjectAddress]*deletionRoot)
			}
			data.ends[ref.Address] = ends
		}
		if rows := s.ownWrites[ref.Address]; len(rows) != 0 {
			if data.own == nil {
				data.own = make(map[ids.ObjectAddress][]rowVersion)
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
			data.deleted = make(map[ids.ObjectAddress]*privateDeletionRoot, selected)
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
	captured := newVisibilityView(data)
	if latest {
		s.partitionValidationView = captured
	} else {
		s.view = captured
	}
	return captured, nil
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

func (v *VisibilityView) Tx() ids.TxRef        { return v.read().tx }
func (v *VisibilityView) Command() CommandID   { return v.read().command }
func (v *VisibilityView) Frontier() CommitSeq  { return v.read().frontier }
func (v *VisibilityView) Catalog() CatalogRead { return v.read().catalog }
