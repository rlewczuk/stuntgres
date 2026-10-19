package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func ownershipSuccessor(auth accessView, target authorization.PrincipalRef) error {
	allowed, err := auth.roles.CanSetRole(auth.state.CurrentUser, target)
	if err != nil {
		return err
	}
	if !allowed {
		role, _ := auth.roles.Descriptor(target)
		return sqlError("42501", fmt.Sprintf(`must be able to SET ROLE "%s"`, role.Name))
	}
	return nil
}
func ownershipDatabaseCreate(auth accessView, env accessEnvironment) error {
	return auth.CheckCreate(env, auth.Ref(catalog.CurrentDatabaseAddress))
}

// ownershipEligibility checks that the current user may hand an object to
// target: target must be a SET ROLE successor and may need CREATE on the
// object's namespace. The namespace check runs as target with the caller's
// environment; a namespace CREATE check never reads the reference path.
func ownershipEligibility(auth accessView, env accessEnvironment, target authorization.PrincipalRef, namespace ids.ObjectAddress) error {
	actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
	if err != nil {
		return err
	}
	if actor.Superuser {
		return nil
	}
	if err = ownershipSuccessor(auth, target); err != nil {
		return err
	}
	targetAuth := auth
	targetAuth.state.CurrentUser = target
	if namespace == (ids.ObjectAddress{}) {
		return nil
	}
	return targetAuth.CheckCreate(env, auth.Ref(namespace))
}
func ownershipRoleOperand(spec parser.RoleSpec) (string, bool) {
	switch spec.Identity {
	case "current":
		return "current_user", true
	case "session":
		return "session_user", true
	}
	return spec.Name, false
}
func schemaOwner(auth accessView, name string, identity bool) (authorization.PrincipalRef, error) {
	if name == "" {
		return auth.state.CurrentUser, nil
	}
	if identity {
		switch name {
		case "current_user", "current_role":
			return auth.state.CurrentUser, nil
		case "session_user":
			return auth.state.SessionUser, nil
		}
	}
	role, ok := auth.roles.Lookup(name)
	if !ok {
		return authorization.PrincipalRef{}, sqlError("42704", fmt.Sprintf(`role "%s" does not exist`, name))
	}
	return role.Ref, nil
}
