package explain

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

// Host is the statement the renderer presents. Core implements it on its
// statement host; every call reads the live statement, never a copy.
//
// The renderer is entered with Engine.Mu held. Methods named …Locked need it;
// ReleaseEngineLocked and ReacquireEngine bracket the provider-callback and
// detached-comparison windows, which run with no engine lock held.
type Host interface {
	// Context is the statement's current execution context (cancellation).
	Context() context.Context
	Operation() *session.Operation
	Environments() (sqltype.InputEnvironment, sqltype.OutputEnvironment)
	AdmittedPlannerMethods() plan.PlannerMethods
	// PendingCatalog is the statement's private catalog journal.
	PendingCatalog() *session.Journal
	CatalogDomain() *ids.Domain
	// PartitionCatalog returns current overlaid with the statement's inheritance
	// read, partition topology and pending detach snapshot.
	PartitionCatalog(current catalog.Read) catalog.Read
	EngineStorageLocked() *storage.Store
	PrivateStorageLocked(address ids.ObjectAddress) *storage.Relation
	// ImplicitStatement reports an implicit transaction without a statement
	// catalog journal: the only case where scan presentation may choose a hash
	// index.
	ImplicitStatement() bool
	CheckRestoreEffect() error
	// CurrentAccessLocked is the operation's current authorization, for fences.
	CurrentAccessLocked() (access.View, error)
	// AuthorizeViewSourceLocked authorizes a stored view source for invoker and
	// returns its authority running as current.
	AuthorizeViewSourceLocked(ref ids.ObjectRef, definition *stored.View, invoker authorization.PrincipalRef, mark plan.RowMark, current authorization.PrincipalRef) (Authority, error)
	ReleaseEngineLocked()
	ReacquireEngine()
	ReleaseReplacementRead()
}

// Authority is the statement authorization the renderer checks and reads
// through: binding privileges, the access environment of policy checks, and
// the rule authority that rewritten commands carry.
type Authority interface {
	analyze.Privileges
	access.Environment
	view.RuleAuthority
}

// authority keeps the privilege view next to the full authority, so catalog
// and role reads do not go through the interface.
type authority struct {
	access.View
	full Authority
}

func authorityOf(a Authority) authority { return authority{View: a.Access(), full: a} }

func (a authority) RestrictedRelations() settings.RestrictedRelationKinds {
	return a.full.RestrictedRelations()
}

// ruleAuthority recovers the renderer authority a rewritten rule command
// carries.
func ruleAuthority(a view.RuleAuthority) Authority {
	v, ok := a.(Authority)
	if !ok {
		panic(fmt.Sprintf("stuntgres: rule authority %T is not an EXPLAIN authority", a))
	}
	return v
}
