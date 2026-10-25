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
	if !ok { return SQLError("0A000", "restore accepts only generated object ACL repairs") }
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil { e.Mu.Unlock(); return err }
	address := ids.ObjectAddress{ClassID: repair.Class}
	if repair.Class == ids.ProcedureClassID {
		types, failure := CaptureRegtypeInput(auth, op)
		if failure != nil { e.Mu.Unlock(); return failure }
		input, failure := CaptureRoutineInput(auth, types)
		if failure != nil { e.Mu.Unlock(); return failure }
		oid, failure := input.ResolveRegprocedure(op.Context(), repair.Identity)
		if failure != nil { e.Mu.Unlock(); return failure }
		address.ObjectID = ids.OID(oid)
	} else {
		address = auth.Catalog.NameAddress(ids.NameKey{ClassID: repair.Class, Name: repair.Identity})
	}
	old := auth.Catalog.Get(address)
	if old == nil { e.Mu.Unlock(); return fmt.Errorf("stuntgres: missing object ACL repair target") }
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
			if failure != nil { e.Mu.Unlock(); return failure }
			x.Owner = e.RoleStore.Domain.Principal(uint64(oid))
		}
		next, acl, revision = &x, &x.ACL, &x.ObjectRevision
	case *catalog.ForeignObject:
		x := *d; next, acl, revision = &x, &x.ACL, &x.ObjectRevision
	default:
		e.Mu.Unlock(); return fmt.Errorf("stuntgres: invalid object ACL repair target")
	}
	if !repair.Owner {
		*acl = authorization.ACLValue{Explicit: !repair.Absent}
		if !repair.Absent {
			output := regobj.ACLItemOutput{Owner: &e.Catalog.Owner, Roles: auth.Roles}
			acl.Entries, err = regobj.ObjectACL(op.Context(), auth.Catalog, repair.Text, e.CaptureACLItemInput(auth, op), output, access.ClassPrivileges(next))
			if err != nil { e.Mu.Unlock(); return err }
		}
	}
	if err = access.ValidateACL(auth.Roles, next); err != nil { e.Mu.Unlock(); return err }
	*revision, err = e.Catalog.RevisionLocked()
	if err != nil { e.Mu.Unlock(); return err }
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	epoch := auth.Catalog.Epoch()
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err = stage.Replace(next); err != nil { return err }
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil { return err }
	_, err = e.PublishCatalogOperation(op, nil, prepared, PublicationToken{Epoch: epoch})
	return err
}
