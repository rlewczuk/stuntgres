package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/largeobject"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// restoreLargeObjectACL is reachable only through the isolated Restore repair
// admission. Ordinary UPDATE of metadata remains an unsupported catalog write.
func (e *Engine) restoreLargeObjectACL(op *session.Operation, raw *parser.Update) error {
	oid, text, absent, ok := largeobject.ACLRepair(raw)
	if !ok {
		return sqlError("0A000", "restore accepts only generated large object ACL repairs")
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	address := ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: ids.OID(oid)}
	old, ok := auth.Catalog.Get(address).(*catalog.LargeObject)
	if !ok {
		e.mu.Unlock()
		return largeObjectMissingError(ids.OID(oid))
	}
	next := *old
	next.ACL = authorization.ACLValue{Explicit: !absent}
	if !absent {
		output := regobj.ACLItemOutput{Owner: &e.catalog.Owner, Roles: auth.Roles}
		next.ACL.Entries, err = regobj.LargeObjectACL(op.Context(), auth.Catalog, text, e.captureACLItemInput(auth, op), output)
		if err != nil {
			e.mu.Unlock()
			return err
		}
	}
	if err = access.ValidateACL(auth.Roles, &next); err != nil {
		e.mu.Unlock()
		return err
	}
	next.ObjectRevision, err = e.catalog.RevisionLocked()
	if err != nil {
		e.mu.Unlock()
		return err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	epoch := auth.Catalog.Epoch()
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err = stage.Replace(&next); err != nil {
		return err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return err
	}
	_, err = e.publishCatalogOperation(op, nil, prepared, PublicationToken{Epoch: epoch})
	return err
}
