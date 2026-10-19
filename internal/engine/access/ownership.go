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
