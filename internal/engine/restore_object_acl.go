package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// restoreObjectACL runs only in Restore's isolated engine. The public UPDATE
// command cannot reach this literal-only catalog repair capability.
func (e *Engine) restoreObjectACL(op *session.Operation, raw *parser.Update) error {
	repair, ok := control.ParseObjectACLRepair(raw)
	if !ok { return sqlError("0A000", "restore accepts only generated object ACL repairs") }
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil { e.mu.Unlock(); return err }
	address := ids.ObjectAddress{ClassID: repair.Class}
	if repair.Class == ids.ProcedureClassID {
		types, failure := captureRegtypeInput(auth, op)
		if failure != nil { e.mu.Unlock(); return failure }
		input, failure := captureRoutineInput(auth, types)
		if failure != nil { e.mu.Unlock(); return failure }
		oid, failure := input.ResolveRegprocedure(op.Context(), repair.Identity)
		if failure != nil { e.mu.Unlock(); return failure }
		address.ObjectID = ids.OID(oid)
	} else {
		address = auth.Catalog.NameAddress(ids.NameKey{ClassID: repair.Class, Name: repair.Identity})
	}
	old := auth.Catalog.Get(address)
	if old == nil { e.mu.Unlock(); return fmt.Errorf("stuntgres: missing object ACL repair target") }
	var next catalog.Descriptor
	var acl *authorization.ACLValue
	var revision *ids.DefinitionRevision
	switch d := old.(type) {
	case *catalog.Procedure:
		x := *d; next, acl, revision = &x, &x.ACL, &x.ObjectRevision
	case *catalog.Language:
		x := *d
		if repair.Owner {
			oid, failure := (regobj.RoleInput{Roles: auth.Roles}).ResolveRegrole(op.Context(), repair.Text)
			if failure != nil { e.mu.Unlock(); return failure }
			x.Owner = e.roles.Domain.Principal(uint64(oid))
		}
		next, acl, revision = &x, &x.ACL, &x.ObjectRevision
	case *catalog.ForeignObject:
		x := *d; next, acl, revision = &x, &x.ACL, &x.ObjectRevision
	default:
		e.mu.Unlock(); return fmt.Errorf("stuntgres: invalid object ACL repair target")
	}
	if !repair.Owner {
		*acl = authorization.ACLValue{Explicit: !repair.Absent}
		if !repair.Absent {
			output := regobj.ACLItemOutput{Owner: &e.catalog.Owner, Roles: auth.Roles}
			acl.Entries, err = regobj.ObjectACL(op.Context(), auth.Catalog, repair.Text, e.captureACLItemInput(auth, op), output, access.ClassPrivileges(next))
			if err != nil { e.mu.Unlock(); return err }
		}
	}
	if err = access.ValidateACL(auth.Roles, next); err != nil { e.mu.Unlock(); return err }
	*revision, err = e.catalog.RevisionLocked()
	if err != nil { e.mu.Unlock(); return err }
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	epoch := auth.Catalog.Epoch()
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err = stage.Replace(next); err != nil { return err }
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil { return err }
	_, err = e.publishCatalogOperation(op, nil, prepared, PublicationToken{Epoch: epoch})
	return err
}
