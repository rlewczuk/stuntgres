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
	"stuntgres/internal/engine/visibility"
)

func (s *StatementScope) acquireViewSources() (err error) {
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
	hasView := d != nil && (d.RelationKind == catalog.ViewRelation || s.materializedOrZero().refresh && d.RelationKind == catalog.MaterializedViewRelation)
	for _, leaf := range s.querySources {
		if leaf.Relation.RelationKind == catalog.ViewRelation {
			hasView = true
			break
		}
	}
	if !hasView {
		e.mu.Unlock()
		return nil
	}
	seen := make(map[ids.ObjectRef]lock.Mode)
	var refs []ids.ObjectRef
	var retainedViews []*catalog.Relation
	hasMaterializedSource := false
	var visit func(ids.ObjectRef, lock.Mode) error
	visit = func(ref ids.ObjectRef, mode lock.Mode) error {
		prior, visited := seen[ref]
		if ref == (ids.ObjectRef{}) || visited && exec.StrongerQueryRowMode(prior, mode) == prior {
			return nil
		}
		seen[ref] = exec.StrongerQueryRowMode(prior, mode)
		d, ok := catalogView.Relation(ref.Address)
		if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() || !ok {
			return ids.ErrCatalogReplaced
		}
		if s.materializedOrZero().refresh && !visited {
			s.materialized().sources = append(s.materializedOrZero().sources, d)
			if ref != s.lockedRef && d.RelationKind == catalog.MaterializedViewRelation {
				hasMaterializedSource = true
			}
		}
		admitted := ref == s.lockedRef
		admittedMode := lock.Mode(0)
		for _, leaf := range s.querySources {
			if leaf.Ref == ref {
				admitted = true
				admittedMode = exec.StrongerQueryRowMode(admittedMode, leaf.Mark.Mode)
			}
		}
		if !admitted && !visited || admitted && mode != 0 && prior == 0 && admittedMode == 0 {
			refs = append(refs, ref)
		}
		if d.RelationKind == catalog.ViewRelation || s.materializedOrZero().refresh && ref == s.lockedRef && d.RelationKind == catalog.MaterializedViewRelation {
			if ViewOf(d).Temporary() != (ids.SessionToken{}) {
				for old := range ViewOf(d).References() {
					if old.Epoch != e.catalog.state.Epoch() {
						retainedViews = append(retainedViews, d)
						break
					}
				}
			}
			for i, leaf := range ViewOf(d).Sources() {
				use, err := ViewUseReference(catalogView, d, leaf.Ref)
				if err != nil {
					return err
				}
				if err := visit(use, viewLeafRowMark(ViewOf(d), i, plan.RowMark{Mode: mode}).Mode); err != nil {
					return err
				}
			}
			for _, dependency := range ViewOf(d).Dependencies() {
				if dependency.Ref.Address.ClassID == ids.RelationClassID {
					child, err := ViewUseReference(catalogView, d, dependency.Ref)
					if err != nil {
						return err
					}
					child.Address.SubID = 0
					if err := visit(child, 0); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	err = visit(s.lockedRef, 0)
	for _, leaf := range s.querySources {
		if err != nil {
			break
		}
		err = visit(leaf.Ref, leaf.Mark.Mode)
	}
	if err == nil && s.materializedOrZero().refresh && hasMaterializedSource {
		var leaves []exec.Leaf
		for ref := range seen {
			if ref == s.lockedRef {
				continue
			}
			relation := catalogView.MustRelation(ref.Address)
			if relation.RelationKind.Stored() {
				leaves = append(leaves, exec.Leaf{Ref: ref})
			}
		}
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
				s.materialized().sourceView = view.Load()
			}
		}
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if err := s.acquireBoundViewRelationModes(refs, seen); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	catalogView = e.privateCatalogLocked(s.owner.Catalog)
	if s.materializedOrZero().refresh {
		for _, observed := range s.materializedOrZero().sources {
			if !materializedSourceCurrent(s.materializedOrZero().sourceView, observed, catalogView.State(), e.privateStorageLocked(s.owner.Catalog, observed.Address())) {
				return ids.ErrCatalogReplaced
			}
		}
	}
	for _, view := range retainedViews {
		for ref := range ViewOf(view).References() {
			if !ViewReferenceValid(catalogView, view, ref) {
				return ids.ErrCatalogReplaced
			}
		}
	}
	// Membership records an acquired AccessShare grant even when the effective
	// query rowmark is zero. Reuse this accounting only after view validation.
	grants = seen
	return nil
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
		valid := ref.Domain() == e.catalog.Domain && ref.Epoch == e.catalog.state.Epoch() && exists
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
	var refs []ids.ObjectRef
	seen := make(map[ids.ObjectRef]bool)
	add := func(ref ids.ObjectRef) {
		if ref.Address.ClassID != ids.RelationClassID {
			return
		}
		ref.Address.SubID = 0
		if ref == s.lockedRef || seen[ref] {
			return
		}
		for _, leaf := range s.querySources {
			if ref == leaf.Ref {
				return
			}
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	for _, dependency := range b.Dependencies {
		add(dependency.Ref)
	}
	for _, constant := range b.Prepared.SequenceConstants {
		if constant.Represented {
			add(constant.Ref)
		}
	}
	return s.acquireBoundViewRelations(refs)
}
