package partitioncmds

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// CaptureInheritedOwner pairs an ordinary table a private Restore created
// (including inheritance members) with the owner role its directive captured.
//
// A genuine named owner directive followed immediately by the bootstrap context
// carries the captured role identity. Only an immediately following matching
// OWNER can use the pair; absence of a transfer grants no private authority.
func CaptureInheritedOwner(e *core.Engine, op *session.Operation, created ids.ObjectRef, owner authorization.PrincipalRef) (table.RestoredInheritedOwner, error) {
	if err := op.Context().Err(); err != nil {
		return table.RestoredInheritedOwner{}, err
	}
	if owner == (authorization.PrincipalRef{}) {
		return table.RestoredInheritedOwner{}, nil
	}
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		return table.RestoredInheritedOwner{}, err
	}
	e.Mu.Lock()
	trusted := core.PartitionConstraintReplayImport(e, op, auth)
	_, capturedRole := e.ImportTargets[owner]
	e.Mu.Unlock()
	if !trusted || !capturedRole {
		return table.RestoredInheritedOwner{}, core.SQLError("0A000", table.RestoreInheritedOwnerBoundary)
	}
	return table.CaptureInheritedOwner(auth.View, created, owner)
}

// RestoreInheritedOwner admits the ALTER ... OWNER TO that follows an
// ordinary table's creation in a private Restore, only for its captured pair.
func RestoreInheritedOwner(e *core.Engine, op *session.Operation, raw *parser.AlterOwner, pair table.RestoredInheritedOwner) error {
	if err := op.Context().Err(); err != nil {
		return err
	}
	reject := func() error { return core.SQLError("0A000", table.RestoreInheritedOwnerBoundary) }
	if !table.InheritedOwnerShape(raw, pair) {
		return reject()
	}
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		return err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if !core.PartitionConstraintReplayImport(e, op, auth) {
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
