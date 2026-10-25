package engine

import (
	"sort"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"
)

// retainProviders captures the exact closure before waits for phased definition
// consumers. Other callers need only the grants and avoid that allocation.
func (s *StatementScope) AcquireViewSources(retainProviders bool) (providers []exec.Leaf, err error) {
	var grants map[ids.ObjectRef]lock.Mode
	defer func() {
		if err == nil {
			err = s.acquireInheritanceSources(grants)
		}
	}()
	e := s.Engine()
	e.Mu.Lock()
	catalogView := e.PrivateCatalogLocked(s.Owner.Catalog)
	d, _ := catalogView.Relation(s.LockedRef.Address)
	hasView := d != nil && (d.RelationKind == catalog.ViewRelation || s.MaterializedOrZero().Refresh && d.RelationKind == catalog.MaterializedViewRelation)
	for _, leaf := range s.QuerySources {
		if leaf.Relation.RelationKind == catalog.ViewRelation {
			hasView = true
			break
		}
	}
	if !hasView {
		e.Mu.Unlock()
		return nil, nil
	}
	closure, err := view.SourceWalk{Catalog: catalogView, Domain: e.Catalog.Domain, Epoch: e.Catalog.State.Epoch(), Locked: s.LockedRef, Leaves: s.QuerySources, Refresh: s.MaterializedOrZero().Refresh}.Closure()
	if s.MaterializedOrZero().Refresh {
		s.materialized().Sources = append(s.MaterializedOrZero().Sources, closure.Sources...)
	}
	seen, refs, retainedViews := closure.Modes, closure.Refs, closure.Retained
	if err == nil && retainProviders {
		for ref := range seen {
			d := catalogView.MustRelation(ref.Address)
			providers = append(providers, exec.Leaf{Ref: ref, Relation: d, Storage: e.PrivateStorageLocked(s.Owner.Catalog, ref.Address)})
		}
	}
	if err == nil && s.MaterializedOrZero().Refresh && closure.MaterializedSource {
		leaves := closure.StoredLeaves(catalogView, s.LockedRef)
		var physical []ids.ObjectRef
		if err = e.CaptureCommentSnapshotLocked(s); err == nil {
			collectAuth := s.Inquiry.deparseAuth
			collectAuth.Recapture(catalogView)
			physical, err = e.QueryTableRefsLocked(collectAuth, collectAuth.State.CurrentUser, leaves, nil, s.Op.Session.SessionToken, plan.RowMark{})
		}
		if err == nil {
			var view *visibility.View
			view, err = s.AcquireDataViewsLocked(e, physical)
			if err == nil {
				s.materialized().SourceView = view.Load()
			}
		}
	}
	e.Mu.Unlock()
	if err != nil {
		return providers, err
	}
	if err := s.acquireBoundViewRelationModes(refs, seen); err != nil {
		return providers, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	catalogView = e.PrivateCatalogLocked(s.Owner.Catalog)
	if s.MaterializedOrZero().Refresh {
		for _, observed := range s.MaterializedOrZero().Sources {
			if !view.MaterializedSourceCurrent(s.MaterializedOrZero().SourceView, observed, catalogView.State(), e.PrivateStorageLocked(s.Owner.Catalog, observed.Address())) {
				return providers, ids.ErrCatalogReplaced
			}
		}
	}
	for _, view := range retainedViews {
		for ref := range stored.ViewOf(view).References() {
			if !stored.ViewReferenceValid(catalogView, view, ref) {
				return providers, ids.ErrCatalogReplaced
			}
		}
	}
	// Membership records an acquired AccessShare grant even when the effective
	// query rowmark is zero. Reuse this accounting only after view validation.
	grants = seen
	return providers, nil
}

func acquireBoundViewRelations(s *StatementScope, refs []ids.ObjectRef) error {
	return s.acquireBoundViewRelationModes(refs, nil)
}

func (s *StatementScope) acquireBoundViewRelationModes(refs []ids.ObjectRef, modes map[ids.ObjectRef]lock.Mode) error {
	e := s.Engine()
	sort.Slice(refs, func(i, j int) bool { return refs[i].Address.ObjectID < refs[j].Address.ObjectID })
	policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
	for _, ref := range refs {
		e.Mu.Lock()
		d, _ := e.PrivateCatalogLocked(s.Owner.Catalog).Relation(ref.Address)
		unsupported := d != nil && catalog.HierarchyRelation(d) && modes[ref] != 0
		e.Mu.Unlock()
		if unsupported {
			return SQLError("0A000", "row locking partition hierarchies is unsupported")
		}
		mode := lock.AccessShare
		if modes[ref] != 0 {
			mode = lock.RowShare
		}
		request, err := e.Locks.StartAcquire(s.Op.Context(), s.LockTicket, lock.Resource{ObjectRef: ref}, mode, policy)
		if request != nil {
			s.admissionWaited = true
			if len(s.rlsActive) != 0 {
				s.rlsImportWaits++
			}
			if s.ReplacementRead {
				e.ReplacementMu.RUnlock()
				s.ReplacementRead = false
			}
			err = request.Wait()
		}
		if err != nil {
			return err
		}
		if err := s.AcquirePartitionDescendants(ref, lock.AccessShare); err != nil {
			return err
		}
		s.Owner.Mu.Lock()
		implicit := s.Owner.State == session.TxImplicit
		s.Owner.Mu.Unlock()
		if implicit && !s.ReplacementRead {
			e.ReplacementMu.RLock()
			s.ReplacementRead = true
		}
		e.Mu.Lock()
		current, exists := e.PrivateCatalogLocked(s.Owner.Catalog).Find(ref.Address)
		valid := ref.Domain() == e.Catalog.Domain && ref.Epoch == e.Catalog.State.Epoch() && exists
		if valid && d != nil && PreparedDefinitionStamp(current, s.Op.Session) != PreparedDefinitionStamp(d, s.Op.Session) {
			s.prepared.changedDuringAdmission = true
		}
		e.Mu.Unlock()
		if !valid {
			return ids.ErrCatalogReplaced
		}
	}
	return nil
}

func acquireViewDefinitionReferences(s *StatementScope, b analyze.Binding) error {
	return acquireBoundViewRelations(s, view.DefinitionReferences(b, s.LockedRef, s.QuerySources))
}
