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
	auth, err := OperationAuthorization(op)
	if err != nil {
		return table.RestoredInheritedOwner{}, err
	}
	e.Mu.Lock()
	trusted := PartitionConstraintReplayImport(e, op, auth)
	_, capturedRole := e.ImportTargets[owner]
	e.Mu.Unlock()
	if !trusted || !capturedRole {
		return table.RestoredInheritedOwner{}, SQLError("0A000", table.RestoreInheritedOwnerBoundary)
	}
	return table.CaptureInheritedOwner(auth.View, created, owner)
}

func (e *Engine) restoreInheritedOwner(op *session.Operation, raw *parser.AlterOwner, pair table.RestoredInheritedOwner) error {
	if err := op.Context().Err(); err != nil {
		return err
	}
	reject := func() error { return SQLError("0A000", table.RestoreInheritedOwnerBoundary) }
	if !table.InheritedOwnerShape(raw, pair) {
		return reject()
	}
	auth, err := OperationAuthorization(op)
	if err != nil {
		return err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if !PartitionConstraintReplayImport(e, op, auth) {
		return reject()
	}
	if err := table.InheritedOwnerDirective(auth.View, raw, pair); err != nil {
		return err
	}
	ref, err := e.ResolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return err
	}
	return table.InheritedOwnerTarget(auth.Catalog, ref, pair)
}
