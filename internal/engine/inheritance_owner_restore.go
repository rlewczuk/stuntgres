package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

const restoreInheritedOwnerBoundary = "restore accepts only immediately paired inherited declaration ownership"

type restoredInheritedOwner struct {
	ref     ids.ObjectRef
	owner   authorization.PrincipalRef
	creator authorization.PrincipalRef
}

// A genuine named owner directive followed immediately by the bootstrap context
// carries the captured role identity. CREATE/OWNER must be the next exact pair;
// no input can retarget an existing relation or choose an unrelated owner.
func (e *Engine) captureInheritedOwner(op *Operation, created ids.ObjectRef, owner authorization.PrincipalRef) (restoredInheritedOwner, error) {
	if err := op.Context().Err(); err != nil {
		return restoredInheritedOwner{}, err
	}
	if owner == (authorization.PrincipalRef{}) {
		return restoredInheritedOwner{}, nil
	}
	auth, err := op.Authorization()
	if err != nil {
		return restoredInheritedOwner{}, err
	}
	e.mu.Lock()
	trusted := partitionConstraintReplayImport(e, op, auth)
	_, capturedRole := e.importTargets[owner]
	e.mu.Unlock()
	if !trusted || !capturedRole {
		return restoredInheritedOwner{}, sqlError("0A000", restoreInheritedOwnerBoundary)
	}
	table, ok := auth.catalog.Relation(created.Address)
	if !ok || table.Inheritance == nil || table.RelationKind != catalog.TableRelation {
		return restoredInheritedOwner{}, sqlError("0A000", restoreInheritedOwnerBoundary)
	}
	namespace, ok := auth.catalog.Namespace(table.Namespace())
	if !ok || namespace.Temporary != (ids.SessionToken{}) || validateInheritance(auth.catalog, table) != nil {
		return restoredInheritedOwner{}, sqlError("0A000", restoreInheritedOwnerBoundary)
	}
	actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
	if err != nil {
		return restoredInheritedOwner{}, err
	}
	if !actor.Superuser || table.Owner != actor.Ref {
		return restoredInheritedOwner{}, sqlError("0A000", restoreInheritedOwnerBoundary)
	}
	if _, err := auth.roles.Descriptor(owner); err != nil {
		return restoredInheritedOwner{}, err
	}
	return restoredInheritedOwner{created, owner, actor.Ref}, nil
}

func (e *Engine) restoreInheritedOwner(op *Operation, raw *parser.AlterOwner, pair restoredInheritedOwner) error {
	if err := op.Context().Err(); err != nil {
		return err
	}
	reject := func() error { return sqlError("0A000", restoreInheritedOwnerBoundary) }
	if raw.Kind != "table" || raw.IfExists || len(raw.Name.Parts) != 2 || raw.Target.Identity != "" || pair.ref == (ids.ObjectRef{}) {
		return reject()
	}
	auth, err := op.Authorization()
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !partitionConstraintReplayImport(e, op, auth) {
		return reject()
	}
	actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser || actor.Ref != pair.creator {
		return reject()
	}
	role, err := auth.roles.Descriptor(pair.owner)
	if err != nil {
		return err
	}
	if raw.Target.Name != role.Name {
		return reject()
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return err
	}
	if ref != pair.ref {
		return reject()
	}
	table, ok := auth.catalog.Relation(ref.Address)
	if !ok || table.Inheritance == nil || table.Owner != pair.creator {
		return reject()
	}
	namespace, ok := auth.catalog.Namespace(table.Namespace())
	if !ok || namespace.Temporary != (ids.SessionToken{}) || validateInheritance(auth.catalog, table) != nil {
		return reject()
	}
	return nil
}
