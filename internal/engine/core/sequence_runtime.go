package core

import (
	"fmt"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sequence"
)

func (h *statementHost) Sequence(request functions.SequenceRequest) (int64, error) {
	scope := h.scope()
	e := scope.Engine()
	ref := request.Ref
	if request.Name != "lastval" && ref.Domain() == nil {
		ref = e.Catalog.Domain.Ref(scope.TxRef.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: request.OID})
	}
	if request.Name == "lastval" {
		e.Mu.Lock()
		ref = e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), PayloadOf(scope.Op.Session).Sequences.Last())
		e.Mu.Unlock()
		if ref.Address == (ids.ObjectAddress{}) {
			return 0, SQLError("55000", "lastval is not yet defined in this session")
		}
	}
	e.Mu.Lock()
	before, beforeErr := scope.writeAuthorizationLocked(e)
	if beforeErr == nil && scope.Catalog != nil {
		before.Catalog = e.PrivateCatalogLocked(scope.Catalog)
		before.Recapture(before.Catalog)
	}
	if beforeErr == nil && ref.Epoch != before.Catalog.Epoch() && request.Temporary == scope.Op.Session.SessionToken && sequence.TemporaryEpochValid(before.Catalog, ref, request.Temporary) {
		ref.Epoch = before.Catalog.Epoch()
	}
	if beforeErr == nil && (ref.Domain() != e.Catalog.Domain || ref.Epoch != before.Catalog.Epoch()) {
		beforeErr = ids.ErrCatalogReplaced
	}
	if beforeErr == nil {
		d, ok := before.Catalog.Relation(ref.Address)
		if !ok {
			if request.Name == "lastval" {
				beforeErr = SQLError("55000", "lastval is not yet defined in this session")
			} else {
				beforeErr = SQLError("XX000", fmt.Sprintf("could not open relation with OID %d", ref.Address.ObjectID))
			}
		} else {
			beforeErr = sequence.CheckKind(d)
		}
	}
	e.Mu.Unlock()
	if beforeErr != nil {
		return 0, beforeErr
	}
	if err := scope.AcquireSequence(ref, lock.RowExclusive, true); err != nil {
		return 0, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := scope.writeAuthorizationLocked(e)
	if err != nil {
		return 0, err
	}
	if scope.Catalog != nil {
		auth.Catalog = e.PrivateCatalogLocked(scope.Catalog)
		auth.Recapture(auth.Catalog)
	}
	d, ok := auth.Catalog.Relation(ref.Address)
	if !ok {
		if request.Name == "lastval" {
			return 0, SQLError("55000", "lastval is not yet defined in this session")
		}
		return 0, SQLError("XX000", fmt.Sprintf("could not open relation with OID %d", ref.Address.ObjectID))
	}
	if err = sequence.CheckKind(d); err != nil {
		return 0, err
	}
	if ns, ok := auth.Catalog.Namespace(d.Namespace()); ok && ns.Temporary == scope.Op.Session.SessionToken {
		scope.Owner.AccessedTemporary = true
	}
	if !request.Identity {
		if err = auth.checkSequenceAny(ref, sequence.CallPrivileges(request.Name)); err != nil {
			return 0, err
		}
	}
	if readOnly := sequence.ReadOnlyCallError(request.Name); scope.Owner.ReadOnly && readOnly != nil {
		ns, _ := auth.Catalog.Namespace(d.Namespace())
		if ns == nil || ns.Temporary != scope.Op.Session.SessionToken {
			return 0, readOnly
		}
	}
	g := e.SequenceGeneratorLocked(scope, ref.Address)
	if g == nil {
		return 0, fmt.Errorf("stuntgres: sequence state missing")
	}
	return sequence.CallLocked(request, d, g, &PayloadOf(scope.Op.Session).Sequences)
}
