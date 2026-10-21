package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
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
	tbl, ok := auth.Catalog.Relation(created.Address)
	if !ok || tbl.Inheritance == nil || tbl.RelationKind != catalog.TableRelation {
		return table.RestoredInheritedOwner{}, sqlError("0A000", table.RestoreInheritedOwnerBoundary)
	}
	namespace, ok := auth.Catalog.Namespace(tbl.Namespace())
	if !ok || namespace.Temporary != (ids.SessionToken{}) || table.ValidateInheritance(auth.Catalog, tbl) != nil {
		return table.RestoredInheritedOwner{}, sqlError("0A000", table.RestoreInheritedOwnerBoundary)
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return table.RestoredInheritedOwner{}, err
	}
	if !actor.Superuser || tbl.Owner != actor.Ref {
		return table.RestoredInheritedOwner{}, sqlError("0A000", table.RestoreInheritedOwnerBoundary)
	}
	if _, err := auth.Roles.Descriptor(owner); err != nil {
		return table.RestoredInheritedOwner{}, err
	}
	return table.RestoredInheritedOwner{Ref: created, Owner: owner, Creator: actor.Ref}, nil
}

func (e *Engine) restoreInheritedOwner(op *session.Operation, raw *parser.AlterOwner, pair table.RestoredInheritedOwner) error {
	if err := op.Context().Err(); err != nil {
		return err
	}
	reject := func() error { return sqlError("0A000", table.RestoreInheritedOwnerBoundary) }
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
	tbl, ok := auth.Catalog.Relation(ref.Address)
	if !ok || tbl.Inheritance == nil || tbl.Owner != pair.Creator {
		return reject()
	}
	namespace, ok := auth.Catalog.Namespace(tbl.Namespace())
	if !ok || namespace.Temporary != (ids.SessionToken{}) || table.ValidateInheritance(auth.Catalog, tbl) != nil {
		return reject()
	}
	return nil
}
