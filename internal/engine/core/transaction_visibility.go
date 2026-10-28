package core

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
	if err := s.Active(); err != nil {
		return nil, err
	}
	s.Owner.Mu.Lock()
	defer s.Owner.Mu.Unlock()
	if !s.worked || s.Owner.State == session.TxIdle || s.Owner.State == session.TxAborted {
		return nil, errors.New("stuntgres: inactive data view scope")
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return s.acquireDataViewLocked(e, ref)
}

func (s *StatementScope) acquireDataViewLocked(e *Engine, ref ids.ObjectRef) (*visibility.View, error) {
	return s.AcquireDataViewsLocked(e, []ids.ObjectRef{ref})
}

func (s *StatementScope) AcquireDataViewsLocked(e *Engine, refs []ids.ObjectRef) (*visibility.View, error) {
	return s.CaptureDataViewsAtLocked(e, refs, false, nil)
}

// A dynamic relation extends the active snapshot locally, never the outer
// source's immutable view or its frontier.
func (s *StatementScope) CaptureDataViewsAtLocked(e *Engine, refs []ids.ObjectRef, latest bool, base *visibility.Data) (*visibility.View, error) {
	captured, err := s.captureDataRootsAtLocked(e, refs, latest, base, false)
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		if err := e.SerializableReadLocked(s, ref); err != nil {
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
		if changes := s.PendingCatalog(); changes != nil && changes.Storage[address].Rewritten {
			return
		}
		stored := e.PrivateStorageLocked(s.PendingCatalog(), address)
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
	capture := func(mutation *StatementMutation, admitted bool) {
		if !admitted && !parser.ModifyingStatement(mutation.Statement) {
			copy, ok := mutation.Statement.(*parser.Copy)
			if !ok || !copy.From {
				return
			}
		}
		relation, _ := mutation.LockedDescriptor.(*catalog.Relation)
		retain(relation)
		for _, target := range mutation.InheritedTargets {
			retain(target.Relation)
		}
	}
	capture(&s.StatementMutation, s.snapshotTargetAdmission)
	for i := range s.mutations {
		if i != s.ActiveMutation {
			capture(&s.mutations[i], false)
		}
	}
}

// Root retention fixes the statement frontier, but is not itself a relation
// read. Actual source acquisition enrolls its own refs through the wrapper above.
func (s *StatementScope) captureDataRootsAtLocked(e *Engine, refs []ids.ObjectRef, latest bool, base *visibility.Data, fresh bool) (*visibility.View, error) {
	if s.TxRef.Epoch() != e.Catalog.State.Epoch() {
		return nil, ids.ErrCatalogReplaced
	}
	for _, ref := range refs {
		if ref.Domain() != e.Catalog.Domain {
			return nil, errors.New("stuntgres: foreign catalog reference")
		}
		if ref.Epoch != e.Catalog.State.Epoch() {
			return nil, ids.ErrCatalogReplaced
		}
		if e.PrivateStorageLocked(s.PendingCatalog(), ref.Address) == nil {
			return nil, errors.New("stuntgres: relation storage is missing")
		}
	}
	in := session.DataViewInput{
		Published: e.Catalog.State, Owner: &e.Catalog.Owner, Store: &e.Storage, Journal: s.PendingCatalog(),
		SameLineage: view.SameMaterializedLineage, SameRowShape: index.SameRelationRowShape,
	}
	// Runtime-named nested SQL captures its admitted source/RLS closure from
	// the outer frontier without borrowing the outer source's membership set.
	// The first capture installs an invocation-local View; later acquisitions
	// keep the ordinary strict validation path and never recapture its roots.
	nested := !latest && base == nil && s.View == nil && s.sqlRoutine.sourceBase != nil
	if nested {
		base = s.sqlRoutine.sourceBase
	}
	routineBase := !latest && base == nil && s.View == nil && s.sqlRoutine.policy.readOnly && s.sqlRoutine.policy.base != nil
	if routineBase {
		base = s.sqlRoutine.policy.base
	}
	existing := s.View
	if latest {
		existing = s.PartitionsOrZero().ValidationView
	}
	if base != nil {
		existing = nil
	}
	if existing != nil && !fresh {
		if err := in.ValidateView(existing, refs); err != nil {
			return nil, err
		}
		return existing, nil
	}
	if !latest && base == nil {
		if _, err := e.VisibilitySnapshotLocked(s.TxRef, false); err != nil {
			return nil, err
		}
		if err := e.captureStatementSnapshotLocked(s); err != nil {
			return nil, err
		}
	}
	in.CommitSeq = e.Visibility.CommitSeq()
	in.Frontier = in.CommitSeq
	if !latest {
		in.Frontier = s.snapshotFrontier
	}
	if base != nil {
		in.Frontier = base.Frontier
	}
	in.Read = e.PrivateCatalogLocked(s.PendingCatalog())
	if base == nil && !latest {
		var err error
		in.RetainDynamic, err = stored.HasDynamicSourceDependency(in.Read, s.RoutineDependencies, s.QuerySources)
		if err != nil {
			return nil, err
		}
		// A logical INSTEAD endpoint has no physical snapshotTarget. Its
		// synchronous bodies still borrow this command's initial lazy roots.
		if !in.RetainDynamic && s.ViewWrite != nil && s.ViewWrite.Instead {
			in.RetainDynamic = true
		}
		// Installed trigger bodies are reached through mutation targets, not
		// the outer expression dependency manifest. Their lazy reads need the
		// same retained command roots as ordinary SQL/PL routine calls.
		if !in.RetainDynamic && s.snapshotTarget.Relation != nil {
			in.RetainDynamic = len(stored.TriggersOf(s.snapshotTarget.Relation)) != 0
		}
		if !in.RetainDynamic {
			for _, target := range s.snapshotTargets {
				if len(stored.TriggersOf(target.Relation)) != 0 {
					in.RetainDynamic = true
					break
				}
			}
		}
	}
	in.Base, in.Latest, in.Stable = base, latest, s.Owner.Isolation.Stable()
	in.Tx, in.Command = s.TxRef, s.CommandID
	in.Roots, in.Target, in.Targets = s.Owner.Roots, s.snapshotTarget, s.snapshotTargets
	if !latest && base == nil && s.sqlRoutine.policy.active && !s.sqlRoutine.policy.readOnly && !in.Stable {
		// Each reached volatile command has a fresh RC frontier. The lexical
		// View fixes it for that command; transaction-stable isolation still
		// uses the transaction roots. Outer mutation-target pins are not this
		// nested command's source snapshot.
		in.Frontier = in.CommitSeq
		in.Target, in.Targets = visibility.Root{}, nil
	}
	in.OwnWrites, in.OwnDeletes = s.OwnWrites, s.OwnDeletes
	captured, err := in.Capture(refs)
	if err != nil {
		return nil, err
	}
	if !fresh {
		if latest {
			s.partitions().ValidationView = captured
		} else if base == nil || nested || routineBase {
			s.View = captured
		}
	}
	return captured, nil
}

// CaptureMaintenanceDataViewsLocked owns an independent per-target capture at
// the statement frontier. Unlike the query cache, releasing it cannot poison a
// later maintenance target or substitute that target's membership set.
func (s *StatementScope) CaptureMaintenanceDataViewsLocked(e *Engine, refs []ids.ObjectRef) (*visibility.View, error) {
	captured, err := s.captureDataRootsAtLocked(e, refs, false, nil, true)
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		if err := e.SerializableReadLocked(s, ref); err != nil {
			captured.Release()
			return nil, err
		}
	}
	return captured, nil
}
