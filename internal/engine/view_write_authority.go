package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

// Caller rights are checked on the original logical target. Each nested layer
// then uses its current owner or the original invoker, never the outer owner.
func (v AuthorizationView) CheckViewWrite(b analyze.Binding, stmt parser.Statement) error {
	w := view.ViewWriteOf(b.ViewWrite)
	logical := w.Logical
	if err := analyze.CheckBinding(v, logical, stmt); err != nil {
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
	for _, layer := range w.Layers {
		current, ok := v.Catalog.Relation(layer.Ref.Address)
		if !ok || current.View != layer.Relation.View {
			return ids.ErrCatalogReplaced
		}
		authority := v
		authority.State.CurrentUser = current.Owner
		if stored.ViewOf(current).Invoker() {
			authority.State.CurrentUser = invoker
		}
		source, ok := v.Catalog.Relation(layer.SourceRef.Address)
		if !ok || !index.SameRelationRowShape(source, layer.Source) {
			return ids.ErrCatalogReplaced
		}
		if mask == authorization.DeletePrivilege {
			if err := authority.Check(access.Requirement{Object: layer.SourceRef, Privileges: mask}); err != nil {
				return err
			}
		} else if err := authority.CheckColumns(layer.SourceRef, source, mask, layer.Targets); err != nil {
			return err
		}
		for occurrence, leaf := range stored.ViewOf(current).Sources() {
			// Only the rewritten main target inherits outer command reads.
			// Independent subquery occurrences keep their own SELECT record,
			// including another occurrence of the same physical relation.
			if occurrence == layer.MainOccurrence && !reads {
				continue
			}
			relation, ok := v.Catalog.Relation(leaf.Ref.Address)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			use, err := stored.ViewUseReference(v.Catalog, current, leaf.Ref)
			if err != nil {
				return err
			}
			indices := make([]int, 0, len(leaf.Reads))
			for _, attr := range leaf.Reads {
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
			if err := authority.CheckColumns(use, relation, authorization.SelectPrivilege, indices); err != nil {
				return err
			}
			if occurrence == layer.MainOccurrence {
				continue // The next write layer owns main-source expansion.
			}
			mark := viewLeafRowMark(stored.ViewOf(current), occurrence, plan.RowMark{})
			if mark.Mode != 0 {
				if err := authority.CheckColumns(use, relation, authorization.UpdatePrivilege, nil); err != nil {
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
func (s *StatementScope) acquireViewWriteTarget(w *view.Write) error {
	e := s.engine()
	policy := lock.Policy{TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: w.Ref}, lock.RowExclusive, policy)
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
	s.owner.Mu.Lock()
	implicit := s.owner.State == session.TxImplicit
	s.owner.Mu.Unlock()
	if implicit && !s.replacementRead {
		e.replacementMu.RLock()
		s.replacementRead = true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	read := e.privateCatalogLocked(s.owner.Catalog)
	current, ok := read.Relation(w.Ref.Address)
	if !ok || w.Ref.Domain() != e.catalog.Domain || w.Ref.Epoch != e.catalog.state.Epoch() || !index.SameRelationRowShape(current, w.BaseRelation) {
		return ids.ErrCatalogReplaced
	}
	for _, layer := range w.Layers {
		d, ok := read.Relation(layer.Ref.Address)
		if !ok || d.View != layer.Relation.View || !index.SameRelationRowShape(d, layer.Relation) {
			return ids.ErrCatalogReplaced
		}
	}
	storage := e.privateStorageLocked(s.owner.Catalog, w.Ref.Address)
	if storage == nil && current.RelationKind != catalog.ForeignRelation {
		return ids.ErrCatalogReplaced
	}
	s.ViewWrite = w
	s.lockedRef, s.lockedDescriptor, s.lockedStorage = w.Ref, current, storage
	return nil
}
