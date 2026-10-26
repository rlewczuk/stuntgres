package engine

import (
	"fmt"

	"maps"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// restoreObjectACL runs only in Restore's isolated engine. The public UPDATE
// command cannot reach this literal-only catalog repair capability.
func restoreObjectACL(e *core.Engine, op *session.Operation, raw *parser.Update) error {
	repair, ok := control.ParseObjectACLRepair(raw)
	if !ok {
		return core.SQLError("0A000", "restore accepts only generated object ACL repairs")
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	address := ids.ObjectAddress{ClassID: repair.Class}
	if repair.Class == ids.ProcedureClassID {
		types, failure := core.CaptureRegtypeInput(auth, op)
		if failure != nil {
			e.Mu.Unlock()
			return failure
		}
		input, failure := core.CaptureRoutineInput(auth, types)
		if failure != nil {
			e.Mu.Unlock()
			return failure
		}
		oid, failure := input.ResolveRegprocedure(op.Context(), repair.Identity)
		if failure != nil {
			e.Mu.Unlock()
			return failure
		}
		address.ObjectID = ids.OID(oid)
	} else if repair.Class == ids.TypeClassID {
		input, failure := core.CaptureRegtypeInput(auth, op)
		if failure != nil {
			e.Mu.Unlock()
			return failure
		}
		oid, failure := input.ResolveRegtype(op.Context(), repair.Identity)
		if failure != nil {
			e.Mu.Unlock()
			return failure
		}
		address.ObjectID = ids.OID(oid)
	} else if repair.Class == ids.RelationClassID {
		name, failure := parser.ParseRegclassName(repair.Identity)
		if failure != nil || len(name.Parts) != 2 {
			e.Mu.Unlock()
			return fmt.Errorf("stuntgres: invalid ACL relation identity")
		}
		ref, failure := e.ResolveRelationReference(auth, name, true)
		if failure != nil {
			e.Mu.Unlock()
			return failure
		}
		address = ref.Address
	} else {
		address = auth.Catalog.NameAddress(ids.NameKey{ClassID: repair.Class, Name: repair.Identity})
	}
	old := auth.Catalog.Get(address)
	if old == nil && repair.Class == ids.ParameterClassID {
		old, err = role.GrantParameterDescriptorLocked(e.Settings, auth.Catalog, repair.Identity, &e.RoleStore.Domain, e.Catalog.AllocateOIDLocked, e.Catalog.RevisionLocked)
		if err != nil {
			e.Mu.Unlock()
			return err
		}
	}
	if old == nil {
		e.Mu.Unlock()
		return fmt.Errorf("stuntgres: missing object ACL repair target")
	}
	var next catalog.Descriptor
	var acl *authorization.ACLValue
	var revision *ids.DefinitionRevision
	switch d := old.(type) {
	case *catalog.Relation:
		x := *d
		next, acl, revision = &x, &x.Security.ACL, &x.ObjectRevision
		if repair.Column != "" {
			_, exists := x.Index[repair.Column]
			if !exists {
				e.Mu.Unlock()
				return fmt.Errorf("stuntgres: missing column ACL repair target")
			}
			x.Security.ColumnACL = maps.Clone(d.Security.ColumnACL)
			if x.Security.ColumnACL == nil {
				x.Security.ColumnACL = make(map[int32]authorization.ACLValue)
			}
			columnACL := authorization.ACLValue{}
			acl = &columnACL
		}
	case *catalog.Procedure:
		x := *d
		next, acl, revision = &x, &x.ACL, &x.ObjectRevision
	case *catalog.Language:
		x := *d
		if repair.Owner {
			oid, failure := (regobj.RoleInput{Roles: auth.Roles}).ResolveRegrole(op.Context(), repair.Text)
			if failure != nil {
				e.Mu.Unlock()
				return failure
			}
			x.Owner = e.RoleStore.Domain.Principal(uint64(oid))
		}
		next, acl, revision = &x, &x.ACL, &x.ObjectRevision
	case *catalog.ForeignObject:
		x := *d
		next, acl, revision = &x, &x.ACL, &x.ObjectRevision
	case *catalog.Namespace:
		x := *d
		next, acl, revision = &x, &x.ACL, &x.ObjectRevision
	case *catalog.Tablespace:
		x := *d
		next, acl, revision = &x, &x.ACL, &x.ObjectRevision
	case *catalog.Parameter:
		x := *d
		next, acl, revision = &x, &x.ACL, &x.ObjectRevision
	case *catalog.Type:
		x := *d
		x.ACLSyntheticDefault = false
		next, acl, revision = &x, &x.ACL, &x.ObjectRevision
	default:
		e.Mu.Unlock()
		return fmt.Errorf("stuntgres: invalid object ACL repair target")
	}
	if repair.Owner && repair.Class != ids.LanguageClassID {
		oid, failure := (regobj.RoleInput{Roles: auth.Roles}).ResolveRegrole(op.Context(), repair.Text)
		if failure != nil {
			e.Mu.Unlock()
			return failure
		}
		owner := e.RoleStore.Domain.Principal(uint64(oid))
		switch x := next.(type) {
		case *catalog.Namespace:
			x.Owner = owner
		case *catalog.Tablespace:
			x.Owner = owner
		}
	}
	if !repair.Owner {
		*acl = authorization.ACLValue{Explicit: !repair.Absent}
		if !repair.Absent {
			output := regobj.ACLItemOutput{Owner: &e.Catalog.Owner, Roles: auth.Roles}
			mask := access.ClassPrivileges(next)
			if repair.Column != "" {
				mask = authorization.ColumnGrantPrivileges
			}
			acl.Entries, err = regobj.ObjectACL(op.Context(), auth.Catalog, repair.Text, e.CaptureACLItemInput(auth, op), output, mask)
			if err != nil {
				e.Mu.Unlock()
				return err
			}
		}
	}
	if repair.Column != "" {
		x := next.(*catalog.Relation)
		sub := x.Attributes[x.Index[repair.Column]]
		if acl.Explicit {
			x.Security.ColumnACL[sub] = *acl
		} else {
			delete(x.Security.ColumnACL, sub)
		}
	}
	if err = access.ValidateACL(auth.Roles, next); err != nil {
		e.Mu.Unlock()
		return err
	}
	*revision, err = e.Catalog.RevisionLocked()
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	epoch := auth.Catalog.Epoch()
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if parameter, ok := next.(*catalog.Parameter); ok && catalog.ParameterBaseline(parameter) {
		if existing := stage.Base.Get(next.Address()); existing != nil {
			stage.RecordRemoval(auth.Ref(existing.Address()), existing)
		}
	} else if stage.Base.Get(next.Address()) == nil {
		if err = stage.Create(next); err != nil {
			return err
		}
	} else if err = stage.Replace(next); err != nil {
		return err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return err
	}
	_, err = e.PublishCatalogOperation(op, nil, prepared, core.PublicationToken{Epoch: epoch})
	return err
}
