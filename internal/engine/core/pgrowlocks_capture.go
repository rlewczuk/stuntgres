package core

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
	s, e := h.scope(), h.scope().Engine()
	if err := s.Active(); err != nil {
		return nil, err
	}
	name, err := parser.ParseRegclassName(text)
	if err != nil {
		return nil, SQLError("42602", "invalid name syntax")
	}
	name, err = regobj.ClassQualifiedName(name, text, s.Op.Session.DatabaseName)
	if err != nil {
		return nil, err
	}
	e.Mu.Lock()
	auth, err := s.writeAuthorizationLocked(e)
	var ref ids.ObjectRef
	var descriptor *catalog.Relation
	var stored *storage.Relation
	if err == nil {
		ref, err = resolveRelationSnapshot(auth, name, true)
	}
	if err == nil {
		descriptor, _ = auth.Catalog.Relation(ref.Address)
		stored = e.PrivateStorageLocked(s.Owner.Catalog, ref.Address)
	}
	e.Mu.Unlock()
	if err != nil {
		return nil, err
	}

	resource := lock.Resource{ObjectRef: ref}
	previous := e.Locks.HasAccessShare(s.LockTicket, resource)
	defer e.Locks.ReleaseAddedAccessShare(s.LockTicket, resource, previous)
	policy := lock.Policy{
		TimeoutSlot: &s.Op.LockWait, Timeout: s.Op.LockTimeout,
		StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext,
	}
	request, err := e.Locks.StartAcquire(s.Op.Context(), s.LockTicket, resource, lock.AccessShare, policy)
	if request != nil {
		s.admissionWaited = true
		if s.ReplacementRead {
			e.ReplacementMu.RUnlock()
			s.ReplacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
		return nil, err
	}
	s.Owner.Mu.Lock()
	implicit := s.Owner.State == session.TxImplicit
	s.Owner.Mu.Unlock()
	if implicit && !s.ReplacementRead {
		e.ReplacementMu.RLock()
		s.ReplacementRead = true
	}

	s.Owner.Mu.Lock()
	e.Mu.Lock()
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
		if s.View != nil {
			if _, captured := s.View.Load().Committed[ref.Address]; !captured {
				view, err = s.CaptureDataViewsAtLocked(e, []ids.ObjectRef{ref}, false, s.View.Load())
				local = true
			} else {
				view, err = s.AcquireDataViewLocked(e, ref)
			}
		} else {
			view, err = s.AcquireDataViewLocked(e, ref)
		}
	}
	e.Mu.Unlock()
	s.Owner.Mu.Unlock()
	if err != nil {
		return nil, err
	}
	if local {
		defer view.Release()
	}
	tuples, err := exec.PgrowlocksPrefix(s.Op.Context(), view, ref)
	if err != nil {
		return nil, err
	}
	records, err := e.Locks.Pgrowlocks(s.Op.Context(), s.LockTicket, tuples)
	if err != nil {
		return nil, err
	}
	e.Mu.Lock()
	valid := e.Catalog.State.Epoch() == ref.Epoch &&
		e.PrivateCatalogLocked(s.Owner.Catalog).Get(ref.Address) == descriptor &&
		e.PrivateStorageLocked(s.Owner.Catalog, ref.Address) == stored
	e.Mu.Unlock()
	if !valid {
		return nil, ids.ErrCatalogReplaced
	}
	return exec.PgrowlocksValues(s.Op.Context(), result, records)
}

// Engine.mu is held; an admission wait never rebinds the lexical name to a
// replacement incarnation, even if the original descriptor was dropped.
func (s *StatementScope) pgrowlocksReadmitLocked(e *Engine, auth AuthorizationView, name parser.ObjectName, ref ids.ObjectRef, descriptor *catalog.Relation, stored *storage.Relation) error {
	if ref.Domain() != e.Catalog.Domain || ref.Epoch != auth.Catalog.Epoch() ||
		auth.Catalog.Get(ref.Address) != descriptor || e.PrivateStorageLocked(s.Owner.Catalog, ref.Address) != stored {
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
