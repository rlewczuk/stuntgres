package engine

import (
	"fmt"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sequence"
)

func (h *statementHost) Sequence(request functions.SequenceRequest) (int64, error) {
	scope := h.scope()
	e := scope.engine()
	ref := request.Ref
	if request.Name != "lastval" && ref.Domain() == nil {
		ref = e.catalog.Domain.Ref(scope.txRef.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: request.OID})
	}
	if request.Name == "lastval" {
		e.mu.Lock()
		ref = e.catalog.Domain.Ref(e.catalog.State.Epoch(), payloadOf(scope.op.Session).sequences.Last())
		e.mu.Unlock()
		if ref.Address == (ids.ObjectAddress{}) {
			return 0, sqlError("55000", "lastval is not yet defined in this session")
		}
	}
	e.mu.Lock()
	before, beforeErr := scope.writeAuthorizationLocked(e)
	if beforeErr == nil && scope.catalog != nil {
		before.Catalog = e.privateCatalogLocked(scope.catalog)
		before.Recapture(before.Catalog)
	}
	if beforeErr == nil && ref.Epoch != before.Catalog.Epoch() && request.Temporary == scope.op.Session.SessionToken && sequence.TemporaryEpochValid(before.Catalog, ref, request.Temporary) {
		ref.Epoch = before.Catalog.Epoch()
	}
	if beforeErr == nil && (ref.Domain() != e.catalog.Domain || ref.Epoch != before.Catalog.Epoch()) {
		beforeErr = ids.ErrCatalogReplaced
	}
	if beforeErr == nil {
		d, ok := before.Catalog.Relation(ref.Address)
		if !ok {
			if request.Name == "lastval" {
				beforeErr = sqlError("55000", "lastval is not yet defined in this session")
			} else {
				beforeErr = sqlError("XX000", fmt.Sprintf("could not open relation with OID %d", ref.Address.ObjectID))
			}
		} else {
			beforeErr = sequence.CheckKind(d)
		}
	}
	e.mu.Unlock()
	if beforeErr != nil {
		return 0, beforeErr
	}
	if err := scope.acquireSequence(ref, lock.RowExclusive, true); err != nil {
		return 0, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := scope.writeAuthorizationLocked(e)
	if err != nil {
		return 0, err
	}
	if scope.catalog != nil {
		auth.Catalog = e.privateCatalogLocked(scope.catalog)
		auth.Recapture(auth.Catalog)
	}
	d, ok := auth.Catalog.Relation(ref.Address)
	if !ok {
		if request.Name == "lastval" {
			return 0, sqlError("55000", "lastval is not yet defined in this session")
		}
		return 0, sqlError("XX000", fmt.Sprintf("could not open relation with OID %d", ref.Address.ObjectID))
	}
	if err = sequence.CheckKind(d); err != nil {
		return 0, err
	}
	if ns, ok := auth.Catalog.Namespace(d.Namespace()); ok && ns.Temporary == scope.op.Session.SessionToken {
		scope.owner.AccessedTemporary = true
	}
	if !request.Identity {
		if err = auth.checkSequenceAny(ref, sequence.CallPrivileges(request.Name)); err != nil {
			return 0, err
		}
	}
	if readOnly := sequence.ReadOnlyCallError(request.Name); scope.owner.ReadOnly && readOnly != nil {
		ns, _ := auth.Catalog.Namespace(d.Namespace())
		if ns == nil || ns.Temporary != scope.op.Session.SessionToken {
			return 0, readOnly
		}
	}
	g := e.sequenceGeneratorLocked(scope, ref.Address)
	if g == nil {
		return 0, fmt.Errorf("stuntgres: sequence state missing")
	}
	return sequence.CallLocked(request, d, g, &payloadOf(scope.op.Session).sequences)
}
