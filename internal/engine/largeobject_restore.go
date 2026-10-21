package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/largeobject"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
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
		array, ok := auth.Catalog.LookupType(sqltype.ACLItemArray)
		if !ok {
			e.mu.Unlock()
			return fmt.Errorf("stuntgres: missing ACL array type")
		}
		value, inputErr := array.InputWithEnvironment(op.Context(), text, -1, sqltype.InputEnvironment{ACLItem: e.captureACLItemInput(auth, op)})
		if inputErr != nil {
			e.mu.Unlock()
			return inputErr
		}
		canonical, outputErr := array.OutputWithEnvironment(op.Context(), value, sqltype.OutputEnvironment{ACLItem: regobj.ACLItemOutput{Owner: e.catalog.owner(), Roles: auth.Roles}})
		if outputErr != nil {
			e.mu.Unlock()
			return outputErr
		}
		if canonical != text {
			e.mu.Unlock()
			return sqlError("0A000", "restore accepts only canonical large object ACL arrays")
		}
		selectBits, _ := authorization.ACLItemBits(authorization.SelectPrivilege)
		updateBits, _ := authorization.ACLItemBits(authorization.UpdatePrivilege)
		privileges := func(bits uint32) authorization.Privileges {
			var out authorization.Privileges
			if bits&selectBits != 0 {
				out |= authorization.SelectPrivilege
			}
			if bits&updateBits != 0 {
				out |= authorization.UpdatePrivilege
			}
			return out
		}
		next.ACL.Entries = make([]authorization.ACLEntry, value.ArrayLen())
		for i := range value.ArrayLen() {
			authority, grantee, grantor, rights, options, itemErr := value.ArrayElement(i).ACLItem()
			if itemErr != nil {
				e.mu.Unlock()
				return itemErr
			}
			if authority != e.catalog.ACLAuthority || rights & ^(selectBits|updateBits) != 0 {
				e.mu.Unlock()
				return fmt.Errorf("stuntgres: invalid large object ACL privileges")
			}
			entry := authorization.ACLEntry{Grantor: e.roles.domain.Principal(uint64(grantor)), Privileges: privileges(rights), GrantOptions: privileges(options)}
			entry.Grantee.Public = grantee == 0
			if grantee != 0 {
				entry.Grantee.Role = e.roles.domain.Principal(uint64(grantee))
			}
			next.ACL.Entries[i] = entry
		}
	}
	if err = access.ValidateACL(auth.Roles, &next); err != nil {
		e.mu.Unlock()
		return err
	}
	next.ObjectRevision, err = e.catalog.revisionLocked()
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
