package access

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// OwnershipSuccessor checks that the current user can SET ROLE to target.
func OwnershipSuccessor(auth View, target authorization.PrincipalRef) error {
	allowed, err := auth.Roles.CanSetRole(auth.State.CurrentUser, target)
	if err != nil {
		return err
	}
	if !allowed {
		role, _ := auth.Roles.Descriptor(target)
		return sqlError("42501", fmt.Sprintf(`must be able to SET ROLE "%s"`, role.Name))
	}
	return nil
}

// OwnershipDatabaseCreate checks CREATE on the current database.
func OwnershipDatabaseCreate(auth View, env Environment) error {
	return auth.CheckCreate(env, auth.Ref(catalog.CurrentDatabaseAddress))
}

// OwnershipEligibility checks that the current user may hand an object to
// target: target must be a SET ROLE successor and may need CREATE on the
// object's namespace. The namespace check runs as target with the caller's
// environment; a namespace CREATE check never reads the reference path.
func OwnershipEligibility(auth View, env Environment, target authorization.PrincipalRef, namespace ids.ObjectAddress) error {
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return err
	}
	if actor.Superuser {
		return nil
	}
	if err = OwnershipSuccessor(auth, target); err != nil {
		return err
	}
	targetAuth := auth
	targetAuth.State.CurrentUser = target
	if namespace == (ids.ObjectAddress{}) {
		return nil
	}
	return targetAuth.CheckCreate(env, auth.Ref(namespace))
}

// OwnershipRoleOperand maps a parser role spec to the role name protocol of
// SchemaOwner: current_user and session_user stand for the identities.
func OwnershipRoleOperand(spec parser.RoleSpec) (string, bool) {
	switch spec.Identity {
	case "current":
		return "current_user", true
	case "session":
		return "session_user", true
	}
	return spec.Name, false
}

// SchemaOwner resolves a schema AUTHORIZATION role: the current user when
// name is empty, an identity when identity is set, otherwise a role by name.
func SchemaOwner(auth View, name string, identity bool) (authorization.PrincipalRef, error) {
	if name == "" {
		return auth.State.CurrentUser, nil
	}
	if identity {
		switch name {
		case "current_user", "current_role":
			return auth.State.CurrentUser, nil
		case "session_user":
			return auth.State.SessionUser, nil
		}
	}
	role, ok := auth.Roles.Lookup(name)
	if !ok {
		return authorization.PrincipalRef{}, sqlError("42704", fmt.Sprintf(`role "%s" does not exist`, name))
	}
	return role.Ref, nil
}

// OwnershipRoot reports whether d is a bulk ownership root for REASSIGN and
// DROP OWNED. Root classification follows canonical reciprocal identities,
// never object names. Protected catalog namespaces and immutable providers are
// not bulk roots. operatorSeeded reports the seeded builtin operators.
func OwnershipRoot(view catalog.Read, d catalog.Descriptor, operatorSeeded func(*catalog.Operator) bool) bool {
	switch t := d.(type) {
	case *catalog.ForeignObject:
		return t.Address().ClassID != ids.ForeignMappingClassID || t.MappedRole != (authorization.PrincipalRef{})
	case *catalog.Namespace:
		return t.Temporary == (ids.SessionToken{}) && t.Address().ObjectID != ids.PGCatalogNamespaceOID && t.Address().ObjectID != ids.PGToastNamespaceOID && t.Address().ObjectID != 13229
	case *catalog.Relation:
		return t.RelationKind != catalog.CatalogRelation && t.RelationKind != catalog.IndexRelation && t.RelationKind != catalog.CompositeRelation && (t.Sequence == nil || t.Sequence.Owned == (ids.ObjectAddress{}))
	case *catalog.Type:
		if !t.SQLType.IsUserDefined() || t.SQLType.IsArray() {
			return false
		}
		if t.OwningRelation == (ids.ObjectAddress{}) {
			return true
		}
		r, ok := view.Relation(t.OwningRelation)
		return ok && r.RelationKind == catalog.CompositeRelation
	case *catalog.Collation:
		return !t.Seeded() && t.Owner != (authorization.PrincipalRef{})
	case *catalog.Conversion:
		return !t.Seeded() && t.Owner != (authorization.PrincipalRef{})
	case *catalog.Operator:
		return !operatorSeeded(t)
	case *catalog.Procedure:
		return !t.Seeded() && t.Foreign == nil
	case *catalog.Database, *catalog.Extension, *catalog.TSDictionary:
		return true
	}
	return false
}
