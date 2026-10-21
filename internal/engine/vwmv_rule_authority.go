package engine

import (
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
