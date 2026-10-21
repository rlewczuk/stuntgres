package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// A genuine named owner directive followed immediately by the bootstrap context
// carries the captured role identity. CREATE/OWNER must be the next exact pair;
// no input can retarget an existing relation or choose an unrelated owner.
func (e *Engine) captureInheritedOwner(op *session.Operation, created ids.ObjectRef, owner authorization.PrincipalRef) (restoredInheritedOwner, error) {
	if err := op.Context().Err(); err != nil {
		return restoredInheritedOwner{}, err
	}
	if owner == (authorization.PrincipalRef{}) {
		return restoredInheritedOwner{}, nil
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return restoredInheritedOwner{}, err
	}
	e.mu.Lock()
	trusted := partitionConstraintReplayImport(e, op, auth)
	_, capturedRole := e.importTargets[owner]
	e.mu.Unlock()
	if !trusted || !capturedRole {
		return restoredInheritedOwner{}, sqlError("0A000", RestoreInheritedOwnerBoundary)
	}
	table, ok := auth.Catalog.Relation(created.Address)
	if !ok || table.Inheritance == nil || table.RelationKind != catalog.TableRelation {
		return restoredInheritedOwner{}, sqlError("0A000", RestoreInheritedOwnerBoundary)
	}
	namespace, ok := auth.Catalog.Namespace(table.Namespace())
	if !ok || namespace.Temporary != (ids.SessionToken{}) || ValidateInheritance(auth.Catalog, table) != nil {
		return restoredInheritedOwner{}, sqlError("0A000", RestoreInheritedOwnerBoundary)
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return restoredInheritedOwner{}, err
	}
	if !actor.Superuser || table.Owner != actor.Ref {
		return restoredInheritedOwner{}, sqlError("0A000", RestoreInheritedOwnerBoundary)
	}
	if _, err := auth.Roles.Descriptor(owner); err != nil {
		return restoredInheritedOwner{}, err
	}
	return restoredInheritedOwner{created, owner, actor.Ref}, nil
}

func (e *Engine) restoreInheritedOwner(op *session.Operation, raw *parser.AlterOwner, pair restoredInheritedOwner) error {
	if err := op.Context().Err(); err != nil {
		return err
	}
	reject := func() error { return sqlError("0A000", RestoreInheritedOwnerBoundary) }
	if raw.Kind != "table" || raw.IfExists || len(raw.Name.Parts) != 2 || raw.Target.Identity != "" || pair.Ref == (ids.ObjectRef{}) {
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
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser || actor.Ref != pair.Creator {
		return reject()
	}
	role, err := auth.Roles.Descriptor(pair.Owner)
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
	if ref != pair.Ref {
		return reject()
	}
	table, ok := auth.Catalog.Relation(ref.Address)
	if !ok || table.Inheritance == nil || table.Owner != pair.Creator {
		return reject()
	}
	namespace, ok := auth.Catalog.Namespace(table.Namespace())
	if !ok || namespace.Temporary != (ids.SessionToken{}) || ValidateInheritance(auth.Catalog, table) != nil {
		return reject()
	}
	return nil
}
