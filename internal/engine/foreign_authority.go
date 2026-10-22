package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/dml"
)

// The FK runtime holds the statement authority as a dml.Authority. It is a
// pointer so that it also serves as an exec.Context's RoutineAuthority.
var _ dml.Authority = (*AuthorizationView)(nil)

// ActingAs returns a copy of v running as owner; a zero owner returns v.
func (v *AuthorizationView) ActingAs(owner authorization.PrincipalRef) dml.Authority {
	if owner == (authorization.PrincipalRef{}) {
		return v
	}
	acting := v.actingAs(owner)
	return &acting
}

// actingAs is v with owner as its current user, as an RI action runs as the
// referencing table's owner; a zero owner leaves v unchanged. The principal is
// not revalidated.
func (v AuthorizationView) actingAs(owner authorization.PrincipalRef) AuthorizationView {
	if owner != (authorization.PrincipalRef{}) {
		v.State.CurrentUser = owner
	}
	return v
}

// foreignAuthorizationOf recovers the authorization view behind an FK
// authority. Every dml.Authority is an *AuthorizationView; anything else is a
// programming error.
func foreignAuthorizationOf(a dml.Authority) *AuthorizationView {
	v, ok := a.(*AuthorizationView)
	if !ok {
		panic(fmt.Sprintf("stuntgres: foreign-key authority %T is not an *AuthorizationView", a))
	}
	return v
}
