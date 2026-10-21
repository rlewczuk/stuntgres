package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// A genuine named owner directive followed immediately by the bootstrap context
// carries the captured role identity. CREATE/OWNER must be the next exact pair;
// no input can retarget an existing relation or choose an unrelated owner.
func (e *Engine) captureInheritedOwner(op *session.Operation, created ids.ObjectRef, owner authorization.PrincipalRef) (table.RestoredInheritedOwner, error) {
	if err := op.Context().Err(); err != nil {
		return table.RestoredInheritedOwner{}, err
	}
	if owner == (authorization.PrincipalRef{}) {
		return table.RestoredInheritedOwner{}, nil
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return table.RestoredInheritedOwner{}, err
	}
	e.mu.Lock()
	trusted := partitionConstraintReplayImport(e, op, auth)
	_, capturedRole := e.importTargets[owner]
	e.mu.Unlock()
	if !trusted || !capturedRole {
		return table.RestoredInheritedOwner{}, sqlError("0A000", table.RestoreInheritedOwnerBoundary)
	}
	return CaptureInheritedOwner(auth.View, created, owner)
}

func (e *Engine) restoreInheritedOwner(op *session.Operation, raw *parser.AlterOwner, pair table.RestoredInheritedOwner) error {
	if err := op.Context().Err(); err != nil {
		return err
	}
	reject := func() error { return sqlError("0A000", table.RestoreInheritedOwnerBoundary) }
	if !InheritedOwnerShape(raw, pair) {
		return reject()
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !partitionConstraintReplayImport(e, op, auth) {
		return reject()
	}
	if err := InheritedOwnerDirective(auth.View, raw, pair); err != nil {
		return err
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return err
	}
	return InheritedOwnerTarget(auth.Catalog, ref, pair)
}
