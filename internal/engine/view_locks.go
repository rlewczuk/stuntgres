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
func (s *StatementScope) acquireViewSources(retainProviders bool) (providers []exec.Leaf, err error) {
	var grants map[ids.ObjectRef]lock.Mode
	defer func() {
		if err == nil {
			err = s.acquireInheritanceSources(grants)
		}
	}()
	e := s.engine()
	e.mu.Lock()
	catalogView := e.privateCatalogLocked(s.owner.Catalog)
	d, _ := catalogView.Relation(s.lockedRef.Address)
	hasView := d != nil && (d.RelationKind == catalog.ViewRelation || s.materializedOrZero().Refresh && d.RelationKind == catalog.MaterializedViewRelation)
	for _, leaf := range s.querySources {
		if leaf.Relation.RelationKind == catalog.ViewRelation {
			hasView = true
			break
		}
	}
	if !hasView {
		e.mu.Unlock()
		return nil, nil
	}
	closure, err := view.SourceWalk{Catalog: catalogView, Domain: e.catalog.Domain, Epoch: e.catalog.State.Epoch(), Locked: s.lockedRef, Leaves: s.querySources, Refresh: s.materializedOrZero().Refresh}.Closure()
	if s.materializedOrZero().Refresh {
		s.materialized().Sources = append(s.materializedOrZero().Sources, closure.Sources...)
	}
	seen, refs, retainedViews := closure.Modes, closure.Refs, closure.Retained
	if err == nil && retainProviders {
		for ref := range seen {
			d := catalogView.MustRelation(ref.Address)
			providers = append(providers, exec.Leaf{Ref: ref, Relation: d, Storage: e.privateStorageLocked(s.owner.Catalog, ref.Address)})
		}
	}
	if err == nil && s.materializedOrZero().Refresh && closure.MaterializedSource {
		leaves := closure.StoredLeaves(catalogView, s.lockedRef)
		var physical []ids.ObjectRef
		if err = e.captureCommentSnapshotLocked(s); err == nil {
			collectAuth := s.inquiry.deparseAuth
			collectAuth.Recapture(catalogView)
			physical, err = e.queryTableRefsLocked(collectAuth, collectAuth.State.CurrentUser, leaves, nil, s.op.Session.SessionToken, plan.RowMark{})
		}
		if err == nil {
			var view *visibility.View
			view, err = s.acquireDataViewsLocked(e, physical)
			if err == nil {
				s.materialized().SourceView = view.Load()
			}
		}
	}
	e.mu.Unlock()
	if err != nil {
		return providers, err
	}
	if err := s.acquireBoundViewRelationModes(refs, seen); err != nil {
		return providers, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	catalogView = e.privateCatalogLocked(s.owner.Catalog)
	if s.materializedOrZero().Refresh {
		for _, observed := range s.materializedOrZero().Sources {
			if !view.MaterializedSourceCurrent(s.materializedOrZero().SourceView, observed, catalogView.State(), e.privateStorageLocked(s.owner.Catalog, observed.Address())) {
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

func (s *StatementScope) acquireBoundViewRelations(refs []ids.ObjectRef) error {
	return s.acquireBoundViewRelationModes(refs, nil)
}

func (s *StatementScope) acquireBoundViewRelationModes(refs []ids.ObjectRef, modes map[ids.ObjectRef]lock.Mode) error {
	e := s.engine()
	sort.Slice(refs, func(i, j int) bool { return refs[i].Address.ObjectID < refs[j].Address.ObjectID })
	policy := lock.Policy{TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext}
	for _, ref := range refs {
		e.mu.Lock()
		d, _ := e.privateCatalogLocked(s.owner.Catalog).Relation(ref.Address)
		unsupported := d != nil && catalog.HierarchyRelation(d) && modes[ref] != 0
		e.mu.Unlock()
		if unsupported {
			return sqlError("0A000", "row locking partition hierarchies is unsupported")
		}
		mode := lock.AccessShare
		if modes[ref] != 0 {
			mode = lock.RowShare
		}
		request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: ref}, mode, policy)
		if request != nil {
			s.admissionWaited = true
			if len(s.rlsActive) != 0 {
				s.rlsImportWaits++
			}
			if s.replacementRead {
				e.replacementMu.RUnlock()
				s.replacementRead = false
			}
			err = request.Wait()
		}
		if err != nil {
			return err
		}
		if err := s.acquirePartitionDescendants(ref, lock.AccessShare); err != nil {
			return err
		}
		s.owner.Mu.Lock()
		implicit := s.owner.State == session.TxImplicit
		s.owner.Mu.Unlock()
		if implicit && !s.replacementRead {
			e.replacementMu.RLock()
			s.replacementRead = true
		}
		e.mu.Lock()
		current, exists := e.privateCatalogLocked(s.owner.Catalog).Find(ref.Address)
		valid := ref.Domain() == e.catalog.Domain && ref.Epoch == e.catalog.State.Epoch() && exists
		if valid && d != nil && preparedDefinitionStamp(current, s.op.Session) != preparedDefinitionStamp(d, s.op.Session) {
			s.prepared.changedDuringAdmission = true
		}
		e.mu.Unlock()
		if !valid {
			return ids.ErrCatalogReplaced
		}
	}
	return nil
}

func (s *StatementScope) acquireViewDefinitionReferences(b analyze.Binding) error {
	return s.acquireBoundViewRelations(view.DefinitionReferences(b, s.lockedRef, s.querySources))
}
