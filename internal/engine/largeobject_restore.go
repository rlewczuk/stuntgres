package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/largeobject"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// restoreLargeObjectACL is reachable only through the isolated Restore repair
// admission. Ordinary UPDATE of metadata remains an unsupported catalog write.
func restoreLargeObjectACL(e *core.Engine, op *session.Operation, raw *parser.Update) error {
	oid, text, absent, ok := largeobject.ACLRepair(raw)
	if !ok {
		return core.SQLError("0A000", "restore accepts only generated large object ACL repairs")
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	address := ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: ids.OID(oid)}
	old, ok := auth.Catalog.Get(address).(*catalog.LargeObject)
	if !ok {
		e.Mu.Unlock()
		return core.LargeObjectMissingError(ids.OID(oid))
	}
	next := *old
	next.ACL = authorization.ACLValue{Explicit: !absent}
	if !absent {
		output := regobj.ACLItemOutput{Owner: &e.Catalog.Owner, Roles: auth.Roles}
		next.ACL.Entries, err = regobj.LargeObjectACL(op.Context(), auth.Catalog, text, e.CaptureACLItemInput(auth, op), output)
		if err != nil {
			e.Mu.Unlock()
			return err
		}
	}
	if err = access.ValidateACL(auth.Roles, &next); err != nil {
		e.Mu.Unlock()
		return err
	}
	next.ObjectRevision, err = e.Catalog.RevisionLocked()
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	epoch := auth.Catalog.Epoch()
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err = stage.Replace(&next); err != nil {
		return err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return err
	}
	_, err = e.PublishCatalogOperation(op, nil, prepared, core.PublicationToken{Epoch: epoch})
	return err
}
