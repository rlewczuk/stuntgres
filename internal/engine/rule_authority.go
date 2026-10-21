package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// ruleAuthority is the statement authority a rewritten rule command runs
// under. AuthorizationView implements it; execution recovers the view with
// ruleAuthorizationOf.
type ruleAuthority interface {
	Access() access.View
	AsUser(authorization.PrincipalRef) ruleAuthority   // the rule owner
	StorageLocked(ids.ObjectAddress) *storage.Relation // caller holds Engine.mu
}

var _ ruleAuthority = AuthorizationView{}

// Access returns the privilege view the authority checks against.
func (v AuthorizationView) Access() access.View { return v.View }

// AsUser returns a copy whose current user is principal; v is unchanged.
func (v AuthorizationView) AsUser(principal authorization.PrincipalRef) ruleAuthority {
	v.State.CurrentUser = principal
	return v
}

// StorageLocked returns the storage this authority reads for a relation.
// The caller holds Engine.mu.
func (v AuthorizationView) StorageLocked(a ids.ObjectAddress) *storage.Relation {
	return v.engine.authorizationStorageLocked(v, a)
}

// ruleAuthorizationOf recovers the full authorization view of a rule
// command. Every ruleAuthority is an AuthorizationView; anything else is a
// programming error.
func ruleAuthorizationOf(auth ruleAuthority) AuthorizationView {
	v, ok := auth.(AuthorizationView)
	if !ok {
		panic(fmt.Sprintf("stuntgres: rule authority %T is not an AuthorizationView", auth))
	}
	return v
}
