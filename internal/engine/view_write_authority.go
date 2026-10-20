package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// Caller rights are checked on the original logical target. Each nested layer
// then uses its current owner or the original invoker, never the outer owner.
func (v AuthorizationView) checkViewWrite(b binding, stmt parser.Statement) error {
	w := viewWriteOf(b.ViewWrite)
	logical := w.logical
	if err := v.checkBinding(logical, stmt); err != nil {
		return err
	}
	invoker := v.State.CurrentUser
	mask := authorization.UpdatePrivilege
	switch stmt.(type) {
	case *parser.Insert:
		mask = authorization.InsertPrivilege
	case *parser.Delete:
		mask = authorization.DeletePrivilege
	}
	reads := b.Returning != nil || len(logical.UsedColumns) != 0 || logical.Insert.Conflict != nil && len(logical.Insert.Conflict.Reads) != 0
	for _, layer := range w.layers {
		current, ok := v.Catalog.Relation(layer.ref.Address)
		if !ok || current.View != layer.relation.View {
			return ids.ErrCatalogReplaced
		}
		authority := v
		authority.State.CurrentUser = current.Owner
		if viewOf(current).invoker {
			authority.State.CurrentUser = invoker
		}
		source, ok := v.Catalog.Relation(layer.sourceRef.Address)
		if !ok || !sameRelationRowShape(source, layer.source) {
			return ids.ErrCatalogReplaced
		}
		physical := binding{Relation: source, Ref: layer.sourceRef}
		if mask == authorization.DeletePrivilege {
			if err := authority.Check(access.Requirement{Object: layer.sourceRef, Privileges: mask}); err != nil {
				return err
			}
		} else if err := authority.checkColumns(physical, mask, layer.targets); err != nil {
			return err
		}
		for occurrence, leaf := range viewOf(current).sources {
			// Only the rewritten main target inherits outer command reads.
			// Independent subquery occurrences keep their own SELECT record,
			// including another occurrence of the same physical relation.
			if occurrence == layer.mainOccurrence && !reads {
				continue
			}
			relation, ok := v.Catalog.Relation(leaf.ref.Address)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			use, err := storedViewUseReference(v.Catalog, current, leaf.ref)
			if err != nil {
				return err
			}
			indices := make([]int, 0, len(leaf.reads))
			for _, attr := range leaf.reads {
				found := false
				for i, live := range relation.Attributes {
					if live == attr {
						indices = append(indices, i)
						found = true
						break
					}
				}
				if !found {
					return ids.ErrCatalogReplaced
				}
			}
			sourceBinding := binding{Ref: use, Relation: relation}
			if err := authority.checkColumns(sourceBinding, authorization.SelectPrivilege, indices); err != nil {
				return err
			}
			if occurrence == layer.mainOccurrence {
				continue // The next write layer owns main-source expansion.
			}
			mark := viewLeafRowMark(viewOf(current), occurrence, plan.RowMark{})
			if mark.Mode != 0 {
				if err := authority.checkColumns(sourceBinding, authorization.UpdatePrivilege, nil); err != nil {
					return err
				}
			}
			// This existing visitor checks child definition SELECT records and
			// applicable row marks under each child's owner/original invoker.
			if relation.RelationKind == catalog.ViewRelation {
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
	policy := lock.Policy{TimeoutSlot: &s.op.lockWait, Timeout: s.op.lockTimeout, StatementDeadline: s.op.statementDeadline, Source: s.op.sourceContext}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: w.ref}, lock.RowExclusive, policy)
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
	read := e.privateCatalogLocked(s.owner.catalog)
	current, ok := read.Relation(w.ref.Address)
	if !ok || w.ref.Domain() != e.catalog.Domain || w.ref.Epoch != e.catalog.state.Epoch() || !sameRelationRowShape(current, w.base) {
		return ids.ErrCatalogReplaced
	}
	for _, layer := range w.layers {
		d, ok := read.Relation(layer.ref.Address)
		if !ok || d.View != layer.relation.View || !sameRelationRowShape(d, layer.relation) {
			return ids.ErrCatalogReplaced
		}
	}
	storage := e.privateStorageLocked(s.owner.catalog, w.ref.Address)
	if storage == nil && current.RelationKind != catalog.ForeignRelation {
		return ids.ErrCatalogReplaced
	}
	s.viewWrite = w
	s.lockedRef, s.lockedDescriptor, s.lockedStorage = w.ref, current, storage
	return nil
}
