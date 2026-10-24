package engine

import (
	"errors"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
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
	return s.captureDataViewsAtLocked(e, refs, false, nil)
}

// Partition integrity validation uses the grant-time committed frontier plus
// this owner's earlier commands, without changing its ordinary snapshot.
func (s *StatementScope) acquirePartitionValidationViewsLocked(e *Engine, refs []ids.ObjectRef) (*visibility.View, error) {
	return s.captureDataViewsAtLocked(e, refs, true, nil)
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

// retainSnapshotTargetRootsLocked pairs already-admitted physical mutation
// targets with the RC frontier while Engine.mu still excludes publication.
// The first target is inline; only a distinct additional target needs a map.
// This does not acquire a data view or enroll a read before source admission.
func (s *StatementScope) retainSnapshotTargetRootsLocked(e *Engine) {
	retain := func(relation *catalog.Relation) {
		if relation == nil {
			return
		}
		address := relation.Address()
		if s.snapshotTarget.Relation != nil && s.snapshotTarget.Relation.Address() == address {
			return
		}
		if _, exists := s.snapshotTargets[address]; exists {
			return
		}
		// Private schema projections keep their existing snapshot owner.
		if s.owner.Catalog != nil && s.owner.Catalog.Storage[address].Rewritten {
			return
		}
		stored := e.privateStorageLocked(s.owner.Catalog, address)
		if stored == nil {
			return
		}
		root := visibility.Root{Versions: stored.Versions, Ends: stored.Ends, Storage: stored, Relation: relation}
		if s.snapshotTarget.Relation == nil {
			s.snapshotTarget = root
			return
		}
		if s.snapshotTargets == nil {
			s.snapshotTargets = make(map[ids.ObjectAddress]visibility.Root)
		}
		s.snapshotTargets[address] = root
	}
	capture := func(mutation *statementMutation, admitted bool) {
		if !admitted && !parser.ModifyingStatement(mutation.statement) {
			copy, ok := mutation.statement.(*parser.Copy)
			if !ok || !copy.From {
				return
			}
		}
		relation, _ := mutation.lockedDescriptor.(*catalog.Relation)
		retain(relation)
		for _, target := range mutation.InheritedTargets {
			retain(target.Relation)
		}
	}
	capture(&s.statementMutation, s.snapshotTargetAdmission)
	for i := range s.mutations {
		if i != s.activeMutation {
			capture(&s.mutations[i], false)
		}
	}
}

// Root retention fixes the statement frontier, but is not itself a relation
// read. Actual source acquisition enrolls its own refs through the wrapper above.
func (s *StatementScope) captureDataRootsAtLocked(e *Engine, refs []ids.ObjectRef, latest bool, base *visibility.Data) (*visibility.View, error) {
	if s.txRef.Epoch() != e.catalog.State.Epoch() {
		return nil, ids.ErrCatalogReplaced
	}
	for _, ref := range refs {
		if ref.Domain() != e.catalog.Domain {
			return nil, errors.New("stuntgres: foreign catalog reference")
		}
		if ref.Epoch != e.catalog.State.Epoch() {
			return nil, ids.ErrCatalogReplaced
		}
		if e.privateStorageLocked(s.owner.Catalog, ref.Address) == nil {
			return nil, errors.New("stuntgres: relation storage is missing")
		}
	}
	in := session.DataViewInput{
		Published: e.catalog.State, Owner: &e.catalog.Owner, Store: &e.storage, Journal: s.owner.Catalog,
		SameLineage: view.SameMaterializedLineage, SameRowShape: index.SameRelationRowShape,
	}
	existing := s.view
	if latest {
		existing = s.partitionsOrZero().ValidationView
	}
	if base != nil {
		existing = nil
	}
	if existing != nil {
		if err := in.ValidateView(existing, refs); err != nil {
			return nil, err
		}
		return existing, nil
	}
	if !latest && base == nil {
		if _, err := e.visibilitySnapshotLocked(s.txRef, false); err != nil {
			return nil, err
		}
		if err := e.captureStatementSnapshotLocked(s); err != nil {
			return nil, err
		}
	}
	in.CommitSeq = e.visibility.CommitSeq()
	in.Frontier = in.CommitSeq
	if !latest {
		in.Frontier = s.snapshotFrontier
	}
	if base != nil {
		in.Frontier = base.Frontier
	}
	in.Read = e.privateCatalogLocked(s.owner.Catalog)
	if base == nil && !latest {
		var err error
		in.RetainDynamic, err = stored.HasPgrowlocksDependency(in.Read, s.routineDependencies, s.querySources)
		if err != nil {
			return nil, err
		}
	}
	in.Base, in.Latest, in.Stable = base, latest, s.owner.Isolation.Stable()
	in.Tx, in.Command = s.txRef, s.command
	in.Roots, in.Target, in.Targets = s.owner.Roots, s.snapshotTarget, s.snapshotTargets
	in.OwnWrites, in.OwnDeletes = s.ownWrites, s.ownDeletes
	captured, err := in.Capture(refs)
	if err != nil {
		return nil, err
	}
	if latest {
		s.partitions().ValidationView = captured
	} else if base == nil {
		s.view = captured
	}
	return captured, nil
}
