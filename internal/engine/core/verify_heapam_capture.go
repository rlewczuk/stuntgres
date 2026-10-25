package core

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// VerifyHeapam pins exact regclass/storage authority, borrows a temporary
// AccessShare contribution, and scans retained unfiltered roots outside locks.
func (h *statementHost) VerifyHeapam(args [6]sqltype.Value, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	s, e := h.scope(), h.scope().Engine()
	if err := s.Active(); err != nil {
		return nil, err
	}
	options, err := exec.VerifyHeapamOptions(args)
	if err != nil {
		return nil, err
	}
	e.Mu.Lock()
	auth, err := s.writeAuthorizationLocked(e)
	var ref ids.ObjectRef
	var descriptor *catalog.Relation
	var stored *storage.Relation
	var sequence *storage.SequenceGenerator
	if err == nil {
		ref = e.Catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: options.Relation})
		descriptor, _ = auth.Catalog.Relation(ref.Address)
		if descriptor == nil {
			err = SQLError("XX000", fmt.Sprintf("could not open relation with OID %d", options.Relation))
		} else {
			stored = e.PrivateStorageLocked(s.Owner.Catalog, ref.Address)
			if descriptor.RelationKind == catalog.SequenceRelation {
				sequence = e.SequenceGeneratorLocked(s, ref.Address)
			}
		}
	}
	e.Mu.Unlock()
	if err != nil {
		return nil, err
	}
	resource := lock.Resource{ObjectRef: ref}
	previous := e.Locks.HasAccessShare(s.LockTicket, resource)
	defer e.Locks.ReleaseAddedAccessShare(s.LockTicket, resource, previous)
	policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
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
		err = s.verifyHeapamReadmitLocked(e, auth, ref, descriptor, stored, sequence)
	}
	if err == nil {
		err = exec.CheckVerifyHeapamTarget(auth.Catalog, descriptor, options)
	}
	if err == nil {
		e.markTemporaryAccessLocked(s, ref, temporaryTargetAttempt)
		err = access.CheckTemporaryStorageAccess(auth.Catalog, descriptor, s.Op.Session.SessionToken)
	}
	var view *visibility.View
	local := false
	if err == nil && descriptor.RelationKind != catalog.SequenceRelation {
		if stored == nil {
			err = fmt.Errorf("stuntgres: heap verification storage missing")
		} else if s.View != nil {
			if _, captured := s.View.Load().Committed[ref.Address]; !captured {
				view, err = s.CaptureDataViewsAtLocked(e, []ids.ObjectRef{ref}, false, s.View.Load())
				local = true
			} else {
				view, err = s.AcquireDataViewLocked(e, ref)
			}
		} else {
			view, err = s.AcquireDataViewLocked(e, ref)
		}
	} else if err == nil && sequence == nil {
		err = fmt.Errorf("stuntgres: sequence state missing")
	}
	e.Mu.Unlock()
	s.Owner.Mu.Unlock()
	if local && view != nil {
		defer view.Release()
	}
	if err != nil {
		return nil, err
	}
	var findings []storage.HeapFinding
	if view != nil {
		findings, err = exec.VerifyHeapamPrefix(s.Op.Context(), view, ref, descriptor, options.Stop)
		if err != nil {
			return nil, err
		}
	}
	rows, err := exec.VerifyHeapamValues(s.Op.Context(), result, findings)
	if err != nil {
		return nil, err
	}
	if err := s.Op.Context().Err(); err != nil {
		return nil, err
	}
	e.Mu.Lock()
	valid := e.Catalog.State.Epoch() == ref.Epoch && e.PrivateCatalogLocked(s.Owner.Catalog).Get(ref.Address) == descriptor && e.PrivateStorageLocked(s.Owner.Catalog, ref.Address) == stored
	if descriptor.RelationKind == catalog.SequenceRelation {
		valid = valid && e.SequenceGeneratorLocked(s, ref.Address) == sequence
	}
	e.Mu.Unlock()
	if !valid {
		return nil, ids.ErrCatalogReplaced
	}
	return rows, nil
}

// Engine.mu is held. Waiting never rebinds a captured OID to a new incarnation.
func (s *StatementScope) verifyHeapamReadmitLocked(e *Engine, auth AuthorizationView, ref ids.ObjectRef, descriptor *catalog.Relation, stored *storage.Relation, sequence *storage.SequenceGenerator) error {
	if ref.Domain() != e.Catalog.Domain || ref.Epoch != auth.Catalog.Epoch() || auth.Catalog.Get(ref.Address) != descriptor || e.PrivateStorageLocked(s.Owner.Catalog, ref.Address) != stored {
		return ids.ErrCatalogReplaced
	}
	if descriptor.RelationKind == catalog.SequenceRelation && e.SequenceGeneratorLocked(s, ref.Address) != sequence {
		return ids.ErrCatalogReplaced
	}
	return nil
}
