package core

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/view"
)

var _ view.RuleAuthority = AuthorizationView{}

// Access returns the privilege view the authority checks against.
func (v AuthorizationView) Access() access.View { return v.View }

// RuleReplica reads the operation-effective enum once per selection attempt.
// Rule ownership changes do not change this settings store. Caller holds no
// settings mutex; Engine.Mu may be held (the canonical lock order).
func (v AuthorizationView) RuleReplica() bool {
	if v.settings == nil {
		return false
	}
	v.settings.Mu.Lock()
	replica := v.settings.TypedValue("session_replication_role").Text == "replica"
	v.settings.Mu.Unlock()
	return replica
}

// AsUser returns a copy whose current user is principal; v is unchanged.
func (v AuthorizationView) AsUser(principal authorization.PrincipalRef) view.RuleAuthority {
	v.State.CurrentUser = principal
	return v
}

// StorageLocked returns the storage this authority reads for a relation: its
// session's private storage, else the engine's published storage. The caller
// holds Engine.mu.
func (v AuthorizationView) StorageLocked(a ids.ObjectAddress) *storage.Relation {
	return session.PrivateStorage(v.inputs.Storage, v.PendingCatalog(), a)
}

// ruleAuthorizationOf recovers the full authorization view of a rule
// command. Every view.RuleAuthority is an AuthorizationView; anything else is a
// programming error.
func ruleAuthorizationOf(auth view.RuleAuthority) AuthorizationView {
	v, ok := auth.(AuthorizationView)
	if !ok {
		panic(fmt.Sprintf("stuntgres: rule authority %T is not an AuthorizationView", auth))
	}
	return v
}
