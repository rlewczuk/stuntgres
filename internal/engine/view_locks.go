package engine

import (
	"sort"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func (s *StatementScope) acquireViewSources() (err error) {
	var grants map[ids.ObjectRef]lock.Mode
	defer func() {
		if err == nil {
			err = s.acquireInheritanceSources(grants)
		}
	}()
	e := s.op.session.Engine()
	e.mu.Lock()
	catalog := e.privateCatalogLocked(s.owner.catalog)
	d, _ := catalog.Relation(s.lockedRef.Address)
	hasView := d != nil && (d.relationKind == viewRelation || s.materializedRefresh && d.relationKind == materializedViewRelation)
	for _, leaf := range s.querySources {
		if leaf.relation.relationKind == viewRelation {
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
	var retainedViews []*relationDescriptor
	hasMaterializedSource := false
	var visit func(ids.ObjectRef, lock.Mode) error
	visit = func(ref ids.ObjectRef, mode lock.Mode) error {
		prior, visited := seen[ref]
		if ref == (ids.ObjectRef{}) || visited && strongerQueryRowMode(prior, mode) == prior {
			return nil
		}
		seen[ref] = strongerQueryRowMode(prior, mode)
		d, ok := catalog.Relation(ref.Address)
		if ref.Domain() != e.catalog.domain || ref.Epoch != e.catalog.state.epoch || !ok {
			return ids.ErrCatalogReplaced
		}
		if s.materializedRefresh && !visited {
			s.materializedSources = append(s.materializedSources, d)
			if ref != s.lockedRef && d.relationKind == materializedViewRelation {
				hasMaterializedSource = true
			}
		}
		admitted := ref == s.lockedRef
		admittedMode := lock.Mode(0)
		for _, leaf := range s.querySources {
			if leaf.ref == ref {
				admitted = true
				admittedMode = strongerQueryRowMode(admittedMode, leaf.mark.mode)
			}
		}
		if !admitted && !visited || admitted && mode != 0 && prior == 0 && admittedMode == 0 {
			refs = append(refs, ref)
		}
		if d.relationKind == viewRelation || s.materializedRefresh && ref == s.lockedRef && d.relationKind == materializedViewRelation {
			if d.view.temporary != (ids.SessionToken{}) {
				for old := range d.view.references {
					if old.Epoch != e.catalog.state.epoch {
						retainedViews = append(retainedViews, d)
						break
					}
				}
			}
			for i, leaf := range d.view.sources {
				use, err := storedViewUseReference(catalog, d, leaf.ref)
				if err != nil {
					return err
				}
				if err := visit(use, viewLeafRowMark(d.view, i, queryRowMark{mode: mode}).mode); err != nil {
					return err
				}
			}
			for _, dependency := range d.view.dependencies {
				if dependency.ref.Address.ClassID == ids.RelationClassID {
					child, err := storedViewUseReference(catalog, d, dependency.ref)
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
		err = visit(leaf.ref, leaf.mark.mode)
	}
	if err == nil && s.materializedRefresh && hasMaterializedSource {
		var leaves []queryLeaf
		for ref := range seen {
			if ref == s.lockedRef {
				continue
			}
			relation := catalog.MustRelation(ref.Address)
			if relation.relationKind.stored() {
				leaves = append(leaves, queryLeaf{ref: ref})
			}
		}
		var physical []ids.ObjectRef
		if err = e.captureCommentSnapshotLocked(s); err == nil {
			collectAuth := s.deparseAuth
			collectAuth.recaptureCatalog(catalog)
			physical, err = e.queryTableRefsLocked(collectAuth, collectAuth.state.CurrentUser, leaves, nil, s.op.session.token, queryRowMark{})
		}
		if err == nil {
			var view *VisibilityView
			view, err = s.acquireDataViewsLocked(e, physical)
			if err == nil {
				s.materializedSourceView = view.data.Load()
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
	catalog = e.privateCatalogLocked(s.owner.catalog)
	if s.materializedRefresh {
		for _, observed := range s.materializedSources {
			if !materializedSourceCurrent(s.materializedSourceView, observed, catalog.state, e.privateStorageLocked(s.owner.catalog, observed.Address())) {
				return ids.ErrCatalogReplaced
			}
		}
	}
	for _, view := range retainedViews {
		for ref := range view.view.references {
			if !storedViewReferenceValid(catalog, view, ref) {
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
	e := s.op.session.Engine()
	sort.Slice(refs, func(i, j int) bool { return refs[i].Address.ObjectID < refs[j].Address.ObjectID })
	policy := lock.Policy{TimeoutSlot: &s.op.lockWait, Timeout: s.op.lockTimeout, StatementDeadline: s.op.statementDeadline, Source: s.op.sourceContext}
	for _, ref := range refs {
		e.mu.Lock()
		d, _ := e.privateCatalogLocked(s.owner.catalog).Relation(ref.Address)
		unsupported := d != nil && hierarchyRelation(d) && modes[ref] != 0
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
		s.owner.mu.Lock()
		implicit := s.owner.state == TxImplicit
		s.owner.mu.Unlock()
		if implicit && !s.replacementRead {
			e.replacementMu.RLock()
			s.replacementRead = true
		}
		e.mu.Lock()
		current, exists := e.privateCatalogLocked(s.owner.catalog).Find(ref.Address)
		valid := ref.Domain() == e.catalog.domain && ref.Epoch == e.catalog.state.epoch && exists
		if valid && d != nil && preparedDefinitionStamp(current, s.op.session) != preparedDefinitionStamp(d, s.op.session) {
			s.preparedChangedDuringAdmission = true
		}
		e.mu.Unlock()
		if !valid {
			return ids.ErrCatalogReplaced
		}
	}
	return nil
}

func (s *StatementScope) acquireViewDefinitionReferences(b binding) error {
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
			if ref == leaf.ref {
				return
			}
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	for _, dependency := range b.dependencies {
		add(dependency.ref)
	}
	for _, constant := range b.sequenceConstants {
		if constant.represented {
			add(constant.ref)
		}
	}
	return s.acquireBoundViewRelations(refs)
}
