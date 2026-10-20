package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sequence"
)

func (h *statementHost) Sequence(request functions.SequenceRequest) (int64, error) {
	scope := h.scope()
	e := scope.op.session.Engine()
	ref := request.Ref
	if request.Name != "lastval" && ref.Domain() == nil {
		ref = e.catalog.Domain.Ref(scope.txRef.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: request.OID})
	}
	if request.Name == "lastval" {
		e.mu.Lock()
		ref = e.catalog.Domain.Ref(e.catalog.state.Epoch(), scope.op.session.sequences.Last())
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
	if beforeErr == nil && ref.Epoch != before.Catalog.Epoch() && request.Temporary == scope.op.session.token && temporarySequenceEpochValid(before.Catalog, ref, request.Temporary) {
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
	if ns, ok := auth.Catalog.Namespace(d.Namespace()); ok && ns.Temporary == scope.op.session.token {
		scope.owner.accessedTemporary = true
	}
	mask := authorization.UsagePrivilege | authorization.SelectPrivilege
	if request.Name == "nextval" {
		mask = authorization.UsagePrivilege | authorization.UpdatePrivilege
	}
	if request.Name == "setval" {
		mask = authorization.UpdatePrivilege
	}
	if !request.Identity {
		if err = auth.checkSequenceAny(ref, mask); err != nil {
			return 0, err
		}
	}
	if scope.owner.readOnly && (request.Name == "nextval" || request.Name == "setval") {
		ns, _ := auth.Catalog.Namespace(d.Namespace())
		if ns == nil || ns.Temporary != scope.op.session.token {
			return 0, sqlError("25006", "cannot execute "+request.Name+"() in a read-only transaction")
		}
	}
	session := scope.op.session
	g := e.sequenceGeneratorLocked(scope, ref.Address)
	if g == nil {
		return 0, fmt.Errorf("stuntgres: sequence state missing")
	}
	m := session.sequences.DemandLocked(ref.Address)
	m.ObserveLocked(g.Generation)
	var result int64
	switch request.Name {
	case "currval", "lastval":
		value, valid := m.Current()
		if !valid {
			msg := fmt.Sprintf(`currval of sequence "%s" is not yet defined in this session`, d.Name())
			if request.Name == "lastval" {
				msg = "lastval is not yet defined in this session"
			}
			return 0, sqlError("55000", msg)
		}
		result = value
	case "nextval":
		result, err = m.NextLocked(*d.Sequence, g, d.Name())
		if err != nil {
			return 0, err
		}
		session.sequences.RecordLastLocked(d.Address())
	case "setval":
		result, err = request.Value.Integer()
		if err != nil {
			return 0, err
		}
		if result < d.Sequence.Min || result > d.Sequence.Max {
			return 0, sqlError("22003", fmt.Sprintf(`setval: value %d is out of bounds for sequence "%s" (%d..%d)`, result, d.Name(), d.Sequence.Min, d.Sequence.Max))
		}
		called := true
		if request.HasCalled {
			called, err = request.Called.Boolean()
			if err != nil {
				return 0, err
			}
		}
		g.Last, g.Called = result, called
		m.SetLocked(result, called)
	}
	return result, nil
}
