package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
)

// Caller rights are checked on the original logical target. Each nested layer
// then uses its current owner or the original invoker, never the outer owner.
func (v AuthorizationView) checkViewWrite(b binding, stmt parser.Statement) error {
	w := b.viewWrite
	logical := w.logical
	if err := v.checkBinding(logical, stmt); err != nil {
		return err
	}
	invoker := v.state.CurrentUser
	mask := authorization.UpdatePrivilege
	switch stmt.(type) {
	case *parser.Insert:
		mask = authorization.InsertPrivilege
	case *parser.Delete:
		mask = authorization.DeletePrivilege
	}
	reads := b.returning != nil || len(logical.usedColumns) != 0 || logical.conflict != nil && len(logical.conflict.reads) != 0
	for _, layer := range w.layers {
		current, ok := v.catalog.state.descriptors[layer.ref.Address].(*relationDescriptor)
		if !ok || current.view != layer.relation.view {
			return ErrCatalogReplaced
		}
		authority := v
		authority.state.CurrentUser = current.owner
		if current.view.invoker {
			authority.state.CurrentUser = invoker
		}
		source, ok := v.catalog.state.descriptors[layer.sourceRef.Address].(*relationDescriptor)
		if !ok || !sameRelationRowShape(source, layer.source) {
			return ErrCatalogReplaced
		}
		physical := binding{relation: source, ref: layer.sourceRef}
		if mask == authorization.DeletePrivilege {
			if err := authority.Check(AccessRequirement{Object: layer.sourceRef, Privileges: mask}); err != nil {
				return err
			}
		} else if err := authority.checkColumns(physical, mask, layer.targets); err != nil {
			return err
		}
		for occurrence, leaf := range current.view.sources {
			// Only the rewritten main target inherits outer command reads.
			// Independent subquery occurrences keep their own SELECT record,
			// including another occurrence of the same physical relation.
			if occurrence == layer.mainOccurrence && !reads {
				continue
			}
			relation, ok := v.catalog.state.descriptors[leaf.ref.Address].(*relationDescriptor)
			if !ok {
				return ErrCatalogReplaced
			}
			use, err := storedViewUseReference(v.catalog, current, leaf.ref)
			if err != nil {
				return err
			}
			indices := make([]int, 0, len(leaf.reads))
			for _, attr := range leaf.reads {
				found := false
				for i, live := range relation.attributes {
					if live == attr {
						indices = append(indices, i)
						found = true
						break
					}
				}
				if !found {
					return ErrCatalogReplaced
				}
			}
			sourceBinding := binding{ref: use, relation: relation}
			if err := authority.checkColumns(sourceBinding, authorization.SelectPrivilege, indices); err != nil {
				return err
			}
			if occurrence == layer.mainOccurrence {
				continue // The next write layer owns main-source expansion.
			}
			mark := viewLeafRowMark(current.view, occurrence, queryRowMark{})
			if mark.mode != 0 {
				if err := authority.checkColumns(sourceBinding, authorization.UpdatePrivilege, nil); err != nil {
					return err
				}
			}
			// This existing visitor checks child definition SELECT records and
			// applicable row marks under each child's owner/original invoker.
			if relation.relationKind == viewRelation {
				if err := authority.checkViewRowMarks(relation, mark, invoker); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Called unlocked after logical analysis, before private preflight/capture. The
// same lock ticket/journal owns the real RX grant and all lifecycle source locks.
func (s *StatementScope) acquireViewWriteTarget(w *viewWrite) error {
	e := s.op.session.Engine()
	policy := relationLockPolicy{TimeoutSlot: &s.op.lockWait, Timeout: s.op.lockTimeout, StatementDeadline: s.op.statementDeadline, Source: s.op.sourceContext}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lockResource{ObjectRef: w.ref}, rowExclusive, policy)
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
	s.owner.mu.Lock()
	implicit := s.owner.state == TxImplicit
	s.owner.mu.Unlock()
	if implicit && !s.replacementRead {
		e.replacementMu.RLock()
		s.replacementRead = true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	catalog := e.privateCatalogLocked(s.owner.catalog)
	current, ok := catalog.state.descriptors[w.ref.Address].(*relationDescriptor)
	if !ok || w.ref.Domain() != e.domain || w.ref.Epoch != e.state.epoch || !sameRelationRowShape(current, w.base) {
		return ErrCatalogReplaced
	}
	for _, layer := range w.layers {
		d, ok := catalog.state.descriptors[layer.ref.Address].(*relationDescriptor)
		if !ok || d.view != layer.relation.view || !sameRelationRowShape(d, layer.relation) {
			return ErrCatalogReplaced
		}
	}
	storage := e.privateStorageLocked(s.owner.catalog, w.ref.Address)
	if storage == nil {
		return ErrCatalogReplaced
	}
	s.viewWrite = w
	s.lockedRef, s.lockedDescriptor, s.lockedStorage = w.ref, current, storage
	return nil
}
