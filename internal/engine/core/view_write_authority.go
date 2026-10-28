package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
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
	if err := s.Active(); err != nil {
		return err
	}
	e := s.Engine()
	policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
	request, err := e.Locks.StartAcquire(s.Op.Context(), s.LockTicket, lock.Resource{ObjectRef: w.Ref}, lock.RowExclusive, policy)
	if request != nil {
		s.admissionWaited = true
		if s.ReplacementRead {
			e.ReplacementMu.RUnlock()
			s.ReplacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
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
	defer e.Mu.Unlock()
	read := e.PrivateCatalogLocked(s.PendingCatalog())
	current, ok := read.Relation(w.Ref.Address)
	if !ok || w.Ref.Domain() != e.Catalog.Domain || w.Ref.Epoch != e.Catalog.State.Epoch() || !index.SameRelationRowShape(current, w.BaseRelation) {
		return ids.ErrCatalogReplaced
	}
	for _, layer := range w.Layers {
		d, ok := read.Relation(layer.Ref.Address)
		if !ok || d.View != layer.Relation.View || !index.SameRelationRowShape(d, layer.Relation) {
			return ids.ErrCatalogReplaced
		}
	}
	if w.Instead {
		if w.BaseRelation.RelationKind != catalog.ViewRelation || current.RelationKind != catalog.ViewRelation || current.View != w.BaseRelation.View || current.RowType != w.BaseRelation.RowType || current.Address() != w.Ref.Address {
			return ids.ErrCatalogReplaced
		}
		if w.TriggerEvent != stored.TriggerInsert && w.TriggerEvent != stored.TriggerUpdate && w.TriggerEvent != stored.TriggerDelete {
			return ids.ErrCatalogReplaced
		}
		matching := false
		for _, definition := range stored.TriggersOf(current) {
			p := definition.Parts()
			matching = matching || p.Timing == stored.TriggerInstead && p.Level == stored.TriggerRow && p.Events&w.TriggerEvent != 0
		}
		if !matching {
			return ids.ErrCatalogReplaced
		}
		s.ViewWrite = w
		// Logical admission never supplies replacement storage authority.
		s.LockedRef, s.LockedDescriptor, s.LockedStorage = w.Ref, current, nil
		return nil
	}
	storage := e.PrivateStorageLocked(s.PendingCatalog(), w.Ref.Address)
	if storage == nil && current.RelationKind != catalog.ForeignRelation {
		return ids.ErrCatalogReplaced
	}
	s.ViewWrite = w
	s.LockedRef, s.LockedDescriptor, s.LockedStorage = w.Ref, current, storage
	return nil
}
