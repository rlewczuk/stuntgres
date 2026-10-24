package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Pgrowlocks materializes once, with no manager calls under Engine.mu. The
// relation lock is an exact, temporary contribution rather than SELECT's normal
// transaction-lifetime AccessShare grant.
func (h *statementHost) Pgrowlocks(text string, result *sqltype.Descriptor) (_ []sqltype.Value, err error) {
	// These are runtime extension diagnostics, not SQL argument occurrences.
	defer func() {
		if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Position != "" {
			copy := *diagnostic
			copy.Position = ""
			err = &copy
		}
	}()
	s, e := h.scope(), h.scope().engine()
	if err := s.active(); err != nil {
		return nil, err
	}
	name, err := parser.ParseRegclassName(text)
	if err != nil {
		return nil, sqlError("42602", "invalid name syntax")
	}
	name, err = regobj.ClassQualifiedName(name, text, s.op.Session.DatabaseName)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	auth, err := s.writeAuthorizationLocked(e)
	var ref ids.ObjectRef
	var descriptor *catalog.Relation
	var stored *storage.Relation
	if err == nil {
		ref, err = resolveRelationSnapshot(auth, name, true)
	}
	if err == nil {
		descriptor, _ = auth.Catalog.Relation(ref.Address)
		stored = e.privateStorageLocked(s.owner.Catalog, ref.Address)
	}
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}

	resource := lock.Resource{ObjectRef: ref}
	previous := e.locks.HasAccessShare(s.ticket, resource)
	defer e.locks.ReleaseAddedAccessShare(s.ticket, resource, previous)
	policy := lock.Policy{
		TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout,
		StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext,
	}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, resource, lock.AccessShare, policy)
	if request != nil {
		s.admissionWaited = true
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
		return nil, err
	}
	s.owner.Mu.Lock()
	implicit := s.owner.State == session.TxImplicit
	s.owner.Mu.Unlock()
	if implicit && !s.replacementRead {
		e.replacementMu.RLock()
		s.replacementRead = true
	}

	s.owner.Mu.Lock()
	e.mu.Lock()
	auth, err = s.writeAuthorizationLocked(e)
	if err == nil {
		err = s.pgrowlocksReadmitLocked(e, auth, name, ref, descriptor, stored)
	}
	if err == nil {
		err = exec.CheckPgrowlocksTable(auth.View, auth, ref, descriptor)
	}
	var view *visibility.View
	local := false
	if err == nil {
		if s.view != nil {
			if _, captured := s.view.Load().Committed[ref.Address]; !captured {
				view, err = s.captureDataViewsAtLocked(e, []ids.ObjectRef{ref}, false, s.view.Load())
				local = true
			} else {
				view, err = s.acquireDataViewLocked(e, ref)
			}
		} else {
			view, err = s.acquireDataViewLocked(e, ref)
		}
	}
	e.mu.Unlock()
	s.owner.Mu.Unlock()
	if err != nil {
		return nil, err
	}
	if local {
		defer view.Release()
	}
	tuples, err := exec.PgrowlocksPrefix(s.op.Context(), view, ref)
	if err != nil {
		return nil, err
	}
	records, err := e.locks.Pgrowlocks(s.op.Context(), s.ticket, tuples)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	valid := e.catalog.State.Epoch() == ref.Epoch &&
		e.privateCatalogLocked(s.owner.Catalog).Get(ref.Address) == descriptor &&
		e.privateStorageLocked(s.owner.Catalog, ref.Address) == stored
	e.mu.Unlock()
	if !valid {
		return nil, ids.ErrCatalogReplaced
	}
	return exec.PgrowlocksValues(s.op.Context(), result, records)
}

// Engine.mu is held; an admission wait never rebinds the lexical name to a
// replacement incarnation, even if the original descriptor was dropped.
func (s *StatementScope) pgrowlocksReadmitLocked(e *Engine, auth AuthorizationView, name parser.ObjectName, ref ids.ObjectRef, descriptor *catalog.Relation, stored *storage.Relation) error {
	if ref.Domain() != e.catalog.Domain || ref.Epoch != auth.Catalog.Epoch() ||
		auth.Catalog.Get(ref.Address) != descriptor || e.privateStorageLocked(s.owner.Catalog, ref.Address) != stored {
		return ids.ErrCatalogReplaced
	}
	current, err := resolveRelationSnapshot(auth, name, true)
	if err != nil {
		return err
	}
	if current != ref {
		return ids.ErrCatalogReplaced
	}
	return nil
}
