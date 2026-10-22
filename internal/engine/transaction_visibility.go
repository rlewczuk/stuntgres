package engine

import (
	"errors"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"
)

func (s *StatementScope) AcquireDataView(ref ids.ObjectRef) (*visibility.View, error) {
	if err := s.active(); err != nil {
		return nil, err
	}
	s.owner.Mu.Lock()
	defer s.owner.Mu.Unlock()
	if !s.worked || s.owner.State == session.TxIdle || s.owner.State == session.TxAborted {
		return nil, errors.New("stuntgres: inactive data view scope")
	}
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	return s.acquireDataViewLocked(e, ref)
}

func (s *StatementScope) acquireDataViewLocked(e *Engine, ref ids.ObjectRef) (*visibility.View, error) {
	return s.acquireDataViewsLocked(e, []ids.ObjectRef{ref})
}

func (s *StatementScope) acquireDataViewsLocked(e *Engine, refs []ids.ObjectRef) (*visibility.View, error) {
	return s.captureDataViewsLocked(e, refs, false)
}

// ATTACH validates the grant-time committed frontier plus this owner's earlier
// commands. It does not change the RR/Serializable owner's ordinary snapshot.
func (s *StatementScope) acquirePartitionValidationViewsLocked(e *Engine, refs []ids.ObjectRef) (*visibility.View, error) {
	return s.captureDataViewsLocked(e, refs, true)
}

func (s *StatementScope) captureDataViewsLocked(e *Engine, refs []ids.ObjectRef, latest bool) (*visibility.View, error) {
	return s.captureDataViewsAtLocked(e, refs, latest, nil)
}

// A dynamic relation extends the active snapshot locally, never the outer
// source's immutable view or its frontier.
func (s *StatementScope) captureDataViewsAtLocked(e *Engine, refs []ids.ObjectRef, latest bool, base *visibility.Data) (*visibility.View, error) {
	captured, err := s.captureDataRootsAtLocked(e, refs, latest, base)
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		if err := e.serializableReadLocked(s, ref); err != nil {
			return nil, err
		}
	}
	return captured, nil
}

// Root retention fixes the statement frontier, but is not itself a relation
// read. Actual source acquisition enrolls its own refs through the wrapper above.
func (s *StatementScope) captureDataRootsAtLocked(e *Engine, refs []ids.ObjectRef, latest bool, base *visibility.Data) (*visibility.View, error) {
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
		if e.privateStorageLocked(s.owner.Catalog, ref.Address) == nil {
			return nil, errors.New("stuntgres: relation storage is missing")
		}
	}
	existing := s.view
	if latest {
		existing = s.partitionsOrZero().ValidationView
	}
	if base != nil {
		existing = nil
	}
	if existing != nil {
		data := existing.Load()
		if data == nil {
			return nil, errors.New("stuntgres: visibility view is released")
		}
		for _, ref := range refs {
			if _, ok := data.Committed[ref.Address]; !ok {
				return nil, errors.New("stuntgres: relation is outside statement data view")
			}
			captured := data.GenerationAt(ref.Address)
			current := e.privateStorageLocked(s.owner.Catalog, ref.Address)
			if captured != current {
				oldRelation, _ := data.Catalog.Relation(ref.Address)
				liveRelation, _ := e.privateCatalogLocked(s.owner.Catalog).Relation(ref.Address)
				if !view.SameMaterializedLineage(oldRelation, liveRelation, captured, current) {
					return nil, ids.ErrCatalogReplaced
				}
			}
		}
		return existing, nil
	}
	if !latest && base == nil {
		if _, err := e.visibilitySnapshotLocked(s.txRef, false); err != nil {
			return nil, err
		}
	}
	frontier := e.visibility.CommitSeq()
	if !latest && s.owner.Isolation.Stable() {
		frontier = s.owner.Frontier
	}
	if base != nil {
		frontier = base.Frontier
	}
	catalog := e.privateCatalogLocked(s.owner.Catalog)
	retainDynamic := false
	if base == nil && !latest {
		var err error
		retainDynamic, err = stored.HasPgrowlocksDependency(catalog, s.routineDependencies, s.querySources)
		if err != nil {
			return nil, err
		}
	}
	data := &visibility.Data{
		Tx: s.txRef, Command: s.command, Frontier: frontier,
		Catalog:   catalog,
		Committed: make(map[ids.ObjectAddress][]storage.RowVersion, len(refs)),
	}
	if base != nil {
		data.DynamicRoots = base.DynamicRoots
	} else if retainDynamic {
		data.DynamicRoots = make(map[ids.ObjectAddress]visibility.Root, len(e.storage.Relations))
		for address := range e.storage.Relations {
			stored := e.privateStorageLocked(s.owner.Catalog, address)
			if relation, ok := catalog.Relation(address); ok && stored != nil {
				data.DynamicRoots[address] = visibility.Root{Versions: stored.Versions, Ends: stored.Ends, Storage: stored, Relation: relation}
			}
		}
		if s.owner.Catalog != nil {
			for address := range s.owner.Catalog.Storage {
				stored := e.privateStorageLocked(s.owner.Catalog, address)
				if relation, ok := catalog.Relation(address); ok && stored != nil {
					data.DynamicRoots[address] = visibility.Root{Versions: stored.Versions, Ends: stored.Ends, Storage: stored, Relation: relation}
				}
			}
		}
	}
	if len(refs) > 1 {
		data.Generations = make(map[ids.ObjectAddress]visibility.Generation, len(refs))
	}
	for _, ref := range refs {
		stored := e.privateStorageLocked(s.owner.Catalog, ref.Address)
		versions, ends := stored.Versions, stored.Ends
		needsBirthFilter := false
		var rewrite session.StorageChange
		if s.owner.Catalog != nil {
			rewrite = s.owner.Catalog.Storage[ref.Address]
		}
		if base != nil && (!s.owner.Isolation.Stable() || rewrite.Rewritten) {
			root, ok := base.DynamicRoots[ref.Address]
			if !ok || root.Storage != stored {
				return nil, ids.ErrCatalogReplaced
			}
			versions, ends = root.Versions, root.Ends
		}
		if !latest && s.owner.Isolation.Stable() && !rewrite.Rewritten {
			root, exists := s.owner.Roots[ref.Address]
			if exists {
				current, ok := e.catalog.state.Relation(ref.Address)
				live := e.storage.Relations[ref.Address]
				if !ok || !index.SameRelationRowShape(root.Relation, current) {
					return nil, ids.ErrCatalogReplaced
				}
				if root.Storage == live {
					versions, ends = root.Versions, root.Ends
				} else if view.SameMaterializedLineage(root.Relation, current, root.Storage, live) {
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
				if s.owner.Catalog != nil {
					if shape, changed := s.owner.Catalog.Storage[ref.Address]; changed && shape.HasSnapshot {
						versions = shape.Snapshot
						needsBirthFilter = false // Projected frozen snapshots retain their birth bound.
					}
				}
			} else {
				versions, ends = nil, nil
			}
		}
		if !latest && rewrite.Rewritten && s.owner.Isolation.Stable() {
			if data.RewrittenFrontiers == nil {
				data.RewrittenFrontiers = make(map[ids.ObjectAddress]ids.CommitSeq)
			}
			data.RewrittenFrontiers[ref.Address] = rewrite.Frontier
		}
		data.Committed[ref.Address] = versions
		if data.Generations != nil {
			data.Generations[ref.Address] = visibility.Generation{Storage: stored, NeedsBirthFilter: needsBirthFilter}
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
			data.Deleted = make(map[ids.ObjectAddress]*visibility.PrivateDeletes, selected)
			for _, ref := range refs {
				if root := s.ownDeletes[ref.Address]; root != nil {
					data.Deleted[ref.Address] = root
				}
			}
		}
	}
	captured := visibility.NewView(data)
	if latest {
		s.partitions().ValidationView = captured
	} else if base == nil {
		s.view = captured
	}
	return captured, nil
}
