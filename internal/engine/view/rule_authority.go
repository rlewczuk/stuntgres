package view

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/storage"
)

// RuleAuthority is the statement authority a rewritten rule command runs
// under. AuthorizationView implements it; execution recovers the view with
// ruleAuthorizationOf.
type RuleAuthority interface {
	Access() access.View
	AsUser(authorization.PrincipalRef) RuleAuthority   // the rule owner
	StorageLocked(ids.ObjectAddress) *storage.Relation // caller holds Engine.mu
	RestrictedRelations() settings.RestrictedRelationKinds
	RuleReplica() bool // current operation-effective setting, independent of rule owner
}
