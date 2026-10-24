package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

// CheckViewWrite implements analyze.Privileges for writes through views.
func (v AuthorizationView) CheckViewWrite(b analyze.Binding, stmt parser.Statement) error {
	return view.CheckWrite(v, v.View, v, b, stmt)
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
	if !ok || w.Ref.Domain() != e.catalog.Domain || w.Ref.Epoch != e.catalog.State.Epoch() || !index.SameRelationRowShape(current, w.BaseRelation) {
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
