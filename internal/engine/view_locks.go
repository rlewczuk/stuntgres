package engine

import (
	"sort"
)

func (s *StatementScope) acquireViewSources() error {
	e := s.op.session.Engine()
	e.mu.Lock()
	catalog := e.privateCatalogLocked(s.owner.catalog)
	d, _ := catalog.state.descriptors[s.lockedRef.Address].(*relationDescriptor)
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
	seen := make(map[ObjectRef]relationLockMode)
	var refs []ObjectRef
	var retainedViews []*relationDescriptor
	var visit func(ObjectRef, relationLockMode) error
	visit = func(ref ObjectRef, mode relationLockMode) error {
		prior, visited := seen[ref]
		if ref == (ObjectRef{}) || visited && strongerQueryRowMode(prior, mode) == prior {
			return nil
		}
		seen[ref] = strongerQueryRowMode(prior, mode)
		d, ok := catalog.state.descriptors[ref.Address].(*relationDescriptor)
		if ref.owner != e.owner || ref.Epoch != e.state.epoch || !ok {
			return ErrCatalogReplaced
		}
		if s.materializedRefresh && !visited {
			s.materializedSources = append(s.materializedSources, d)
		}
		admitted := ref == s.lockedRef
		admittedMode := relationLockMode(0)
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
			if d.view.temporary != (SessionToken{}) {
				for old := range d.view.references {
					if old.Epoch != e.state.epoch {
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
				if dependency.ref.Address.ClassID == RelationClassID {
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
	err := visit(s.lockedRef, 0)
	for _, leaf := range s.querySources {
		if err != nil {
			break
		}
		err = visit(leaf.ref, leaf.mark.mode)
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
			if catalog.state.descriptors[observed.Address()] != observed {
				return ErrCatalogReplaced
			}
		}
	}
	for _, view := range retainedViews {
		for ref := range view.view.references {
			if !storedViewReferenceValid(catalog, view, ref) {
				return ErrCatalogReplaced
			}
		}
	}
	return nil
}

func (s *StatementScope) acquireBoundViewRelations(refs []ObjectRef) error {
	return s.acquireBoundViewRelationModes(refs, nil)
}

func (s *StatementScope) acquireBoundViewRelationModes(refs []ObjectRef, modes map[ObjectRef]relationLockMode) error {
	e := s.op.session.Engine()
	sort.Slice(refs, func(i, j int) bool { return refs[i].Address.ObjectID < refs[j].Address.ObjectID })
	policy := relationLockPolicy{operation: s.op, timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext}
	for _, ref := range refs {
		e.mu.Lock()
		d, _ := e.privateCatalogLocked(s.owner.catalog).state.descriptors[ref.Address].(*relationDescriptor)
		unsupported := d != nil && hierarchyRelation(d) && modes[ref] != 0
		e.mu.Unlock()
		if unsupported {
			return sqlError("0A000", "row locking partition hierarchies is unsupported")
		}
		if err := s.acquirePartitionAncestors(ref); err != nil {
			return err
		}
		mode := accessShare
		if modes[ref] != 0 {
			mode = relationRowShare
		}
		request, err := e.locks.startAcquire(s.op.Context(), s.ticket, lockResource{ObjectRef: ref}, mode, policy)
		if request != nil {
			if s.replacementRead {
				e.replacementMu.RUnlock()
				s.replacementRead = false
			}
			err = request.wait()
		}
		if err != nil {
			return err
		}
		if err := s.acquirePartitionDescendants(ref, accessShare); err != nil {
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
		_, exists := e.privateCatalogLocked(s.owner.catalog).state.descriptors[ref.Address]
		valid := ref.owner == e.owner && ref.Epoch == e.state.epoch && exists
		e.mu.Unlock()
		if !valid {
			return ErrCatalogReplaced
		}
	}
	return nil
}

func (s *StatementScope) acquireViewDefinitionReferences(b binding) error {
	var refs []ObjectRef
	seen := make(map[ObjectRef]bool)
	add := func(ref ObjectRef) {
		if ref.Address.ClassID != RelationClassID {
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
