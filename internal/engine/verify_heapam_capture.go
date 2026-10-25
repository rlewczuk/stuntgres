package engine

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
	s, e := h.scope(), h.scope().engine()
	if err := s.active(); err != nil { return nil, err }
	options, err := exec.VerifyHeapamOptions(args)
	if err != nil { return nil, err }
	e.mu.Lock()
	auth, err := s.writeAuthorizationLocked(e)
	var ref ids.ObjectRef
	var descriptor *catalog.Relation
	var stored *storage.Relation
	var sequence *storage.SequenceGenerator
	if err == nil {
		ref = e.catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: options.Relation})
		descriptor, _ = auth.Catalog.Relation(ref.Address)
		if descriptor == nil { err = sqlError("XX000", fmt.Sprintf("could not open relation with OID %d", options.Relation)) } else {
			stored = e.privateStorageLocked(s.owner.Catalog, ref.Address)
			if descriptor.RelationKind == catalog.SequenceRelation { sequence = e.sequenceGeneratorLocked(s, ref.Address) }
		}
	}
	e.mu.Unlock()
	if err != nil { return nil, err }
	resource := lock.Resource{ObjectRef: ref}
	previous := e.locks.HasAccessShare(s.ticket, resource)
	defer e.locks.ReleaseAddedAccessShare(s.ticket, resource, previous)
	policy := lock.Policy{TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, resource, lock.AccessShare, policy)
	if request != nil {
		s.admissionWaited = true
		if s.replacementRead { e.replacementMu.RUnlock(); s.replacementRead = false }
		err = request.Wait()
	}
	if err != nil { return nil, err }
	s.owner.Mu.Lock()
	implicit := s.owner.State == session.TxImplicit
	s.owner.Mu.Unlock()
	if implicit && !s.replacementRead { e.replacementMu.RLock(); s.replacementRead = true }
	s.owner.Mu.Lock()
	e.mu.Lock()
	auth, err = s.writeAuthorizationLocked(e)
	if err == nil { err = s.verifyHeapamReadmitLocked(e, auth, ref, descriptor, stored, sequence) }
	if err == nil { err = exec.CheckVerifyHeapamTarget(auth.Catalog, descriptor, options) }
	if err == nil {
		e.markTemporaryAccessLocked(s, ref, temporaryTargetAttempt)
		err = access.CheckTemporaryStorageAccess(auth.Catalog, descriptor, s.op.Session.SessionToken)
	}
	var view *visibility.View
	local := false
	if err == nil && descriptor.RelationKind != catalog.SequenceRelation {
		if stored == nil { err = fmt.Errorf("stuntgres: heap verification storage missing") } else if s.view != nil {
			if _, captured := s.view.Load().Committed[ref.Address]; !captured {
				view, err = s.captureDataViewsAtLocked(e, []ids.ObjectRef{ref}, false, s.view.Load())
				local = true
			} else { view, err = s.acquireDataViewLocked(e, ref) }
		} else { view, err = s.acquireDataViewLocked(e, ref) }
	} else if err == nil && sequence == nil { err = fmt.Errorf("stuntgres: sequence state missing") }
	e.mu.Unlock()
	s.owner.Mu.Unlock()
	if local && view != nil { defer view.Release() }
	if err != nil { return nil, err }
	var findings []storage.HeapFinding
	if view != nil {
		findings, err = exec.VerifyHeapamPrefix(s.op.Context(), view, ref, descriptor, options.Stop)
		if err != nil { return nil, err }
	}
	rows, err := exec.VerifyHeapamValues(s.op.Context(), result, findings)
	if err != nil { return nil, err }
	if err := s.op.Context().Err(); err != nil { return nil, err }
	e.mu.Lock()
	valid := e.catalog.State.Epoch() == ref.Epoch && e.privateCatalogLocked(s.owner.Catalog).Get(ref.Address) == descriptor && e.privateStorageLocked(s.owner.Catalog, ref.Address) == stored
	if descriptor.RelationKind == catalog.SequenceRelation { valid = valid && e.sequenceGeneratorLocked(s, ref.Address) == sequence }
	e.mu.Unlock()
	if !valid { return nil, ids.ErrCatalogReplaced }
	return rows, nil
}

// Engine.mu is held. Waiting never rebinds a captured OID to a new incarnation.
func (s *StatementScope) verifyHeapamReadmitLocked(e *Engine, auth AuthorizationView, ref ids.ObjectRef, descriptor *catalog.Relation, stored *storage.Relation, sequence *storage.SequenceGenerator) error {
	if ref.Domain() != e.catalog.Domain || ref.Epoch != auth.Catalog.Epoch() || auth.Catalog.Get(ref.Address) != descriptor || e.privateStorageLocked(s.owner.Catalog, ref.Address) != stored { return ids.ErrCatalogReplaced }
	if descriptor.RelationKind == catalog.SequenceRelation && e.sequenceGeneratorLocked(s, ref.Address) != sequence { return ids.ErrCatalogReplaced }
	return nil
}
