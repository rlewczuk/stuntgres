package engine

import (
	"errors"
	"sync/atomic"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// VisibilityView retains exact immutable relation-local roots, not copied row
// values. Each handle has independent release authority. The immutable data is
// naturally reclaimed after the last view/result owner; no registry is needed.
// A committed TRUNCATE incarnation can be newer than the retained snapshot.
// This flag is capture metadata for the existing selector, not another frontier.
type visibilityGeneration struct {
	Storage          *storage.Relation
	NeedsBirthFilter bool
}

type visibilityData struct {
	Tx                     ids.TxRef
	Command                ids.CommandID
	Frontier               ids.CommitSeq
	RewrittenFrontiers     map[ids.ObjectAddress]ids.CommitSeq
	Catalog                catalog.Read
	Committed              map[ids.ObjectAddress][]storage.RowVersion
	Generations            map[ids.ObjectAddress]visibilityGeneration
	SingleGeneration       *storage.Relation
	SingleNeedsBirthFilter bool
	Own                    map[ids.ObjectAddress][]storage.RowVersion
	Ends                   map[ids.ObjectAddress]*storage.DeletionRoot
	Deleted                map[ids.ObjectAddress]*privateDeletionRoot
}

func (d *visibilityData) GenerationAt(address ids.ObjectAddress) *storage.Relation {
	if d.Generations != nil {
		return d.Generations[address].Storage
	}
	if _, ok := d.Committed[address]; ok {
		return d.SingleGeneration
	}
	return nil
}

func (d *visibilityData) NeedsBirthFilterAt(address ids.ObjectAddress) bool {
	if d.Generations != nil {
		return d.Generations[address].NeedsBirthFilter
	}
	_, captured := d.Committed[address]
	return captured && d.SingleNeedsBirthFilter
}

func (d *visibilityData) FrontierAt(address ids.ObjectAddress) ids.CommitSeq {
	if frontier, ok := d.RewrittenFrontiers[address]; ok {
		return frontier
	}
	return d.Frontier
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
	if s.txRef.Epoch() != e.catalog.state.Epoch() {
		return nil, ids.ErrCatalogReplaced
	}
	for _, ref := range refs {
		if ref.Domain() != e.catalog.Domain {
			return nil, errors.New("stuntgres: foreign catalog reference")
		}
		if ref.Epoch != e.catalog.state.Epoch() {
			return nil, ids.ErrCatalogReplaced
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
			if _, ok := data.Committed[ref.Address]; !ok {
				return nil, errors.New("stuntgres: relation is outside statement data view")
			}
			captured := data.GenerationAt(ref.Address)
			current := e.privateStorageLocked(s.owner.catalog, ref.Address)
			if captured != current {
				oldRelation, _ := data.Catalog.Relation(ref.Address)
				liveRelation, _ := e.privateCatalogLocked(s.owner.catalog).Relation(ref.Address)
				if !sameMaterializedLineage(oldRelation, liveRelation, captured, current) {
					return nil, ids.ErrCatalogReplaced
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
	frontier := e.visibility.CommitSeq()
	if !latest && stableIsolation(s.owner.isolation) {
		frontier = s.owner.frontier
	}
	catalog := e.privateCatalogLocked(s.owner.catalog)
	data := &visibilityData{
		Tx: s.txRef, Command: s.command, Frontier: frontier,
		Catalog:   catalog,
		Committed: make(map[ids.ObjectAddress][]storage.RowVersion, len(refs)),
	}
	if len(refs) > 1 {
		data.Generations = make(map[ids.ObjectAddress]visibilityGeneration, len(refs))
	}
	for _, ref := range refs {
		stored := e.privateStorageLocked(s.owner.catalog, ref.Address)
		versions, ends := stored.Versions, stored.Ends
		needsBirthFilter := false
		var rewrite catalogStorageChange
		if s.owner.catalog != nil {
			rewrite = s.owner.catalog.storage[ref.Address]
		}
		if !latest && stableIsolation(s.owner.isolation) && !rewrite.rewritten {
			root, exists := s.owner.roots[ref.Address]
			if exists {
				current, ok := e.catalog.state.Relation(ref.Address)
				live := e.storage.Relations[ref.Address]
				if !ok || !sameRelationRowShape(root.Relation, current) {
					return nil, ids.ErrCatalogReplaced
				}
				if root.Storage == live {
					versions, ends = root.Versions, root.Ends
				} else if sameMaterializedLineage(root.Relation, current, root.Storage, live) {
					// Concurrent MV publication retains the actual RR root and
					// its end markers, not the new incarnation with a birth filter.
					versions, ends = root.Versions, root.Ends
				} else {
					if !root.Storage.SameTruncateLineage(live) {
						return nil, ids.ErrCatalogReplaced
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
			if data.RewrittenFrontiers == nil {
				data.RewrittenFrontiers = make(map[ids.ObjectAddress]ids.CommitSeq)
			}
			data.RewrittenFrontiers[ref.Address] = rewrite.frontier
		}
		data.Committed[ref.Address] = versions
		if data.Generations != nil {
			data.Generations[ref.Address] = visibilityGeneration{Storage: stored, NeedsBirthFilter: needsBirthFilter}
		} else {
			data.SingleGeneration = stored
			data.SingleNeedsBirthFilter = needsBirthFilter
		}
		if ends != nil {
			if data.Ends == nil {
				data.Ends = make(map[ids.ObjectAddress]*storage.DeletionRoot)
			}
			data.Ends[ref.Address] = ends
		}
		if rows := s.ownWrites[ref.Address]; len(rows) != 0 {
			if data.Own == nil {
				data.Own = make(map[ids.ObjectAddress][]storage.RowVersion)
			}
			data.Own[ref.Address] = rows
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
			data.Deleted = s.ownDeletes
		} else if selected != 0 {
			data.Deleted = make(map[ids.ObjectAddress]*privateDeletionRoot, selected)
			for _, ref := range refs {
				if root := s.ownDeletes[ref.Address]; root != nil {
					data.Deleted[ref.Address] = root
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

// Load returns the captured data, or nil once this handle is released.
// Unlike Data it does not panic and does not check the handle's identity.
func (v *VisibilityView) Load() *visibilityData { return v.data.Load() }

func (v *VisibilityView) Data() *visibilityData {
	if v == nil || v.self != v {
		panic("stuntgres: visibility view is released")
	}
	data := v.data.Load()
	if data == nil {
		panic("stuntgres: visibility view is released")
	}
	return data
}

func (v *VisibilityView) Tx() ids.TxRef           { return v.Data().Tx }
func (v *VisibilityView) Command() ids.CommandID  { return v.Data().Command }
func (v *VisibilityView) Frontier() ids.CommitSeq { return v.Data().Frontier }
func (v *VisibilityView) Catalog() catalog.Read   { return v.Data().Catalog }
