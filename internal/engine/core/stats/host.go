package stats

import (
	"context"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Host is the statement a pg_restore_*_stats or pg_clear_*_stats call runs
// in. Core implements it on its statement host; every call reads the live
// statement, never a copy.
//
// An import is entered with no engine lock held. ReacquireEngine takes
// Engine.Mu and ReleaseEngineLocked releases it; the methods named …Locked
// need it. AcquireObjectLock, value parsing and notices run with nothing held.
type Host[A Authority] interface {
	// Operation receives the import's WARNING notices.
	Operation() *session.Operation
	Context() context.Context
	// Environments' input parses statistic arrays.
	Environments() (sqltype.InputEnvironment, sqltype.OutputEnvironment)
	ReacquireEngine()
	ReleaseEngineLocked()
	// PendingCatalog is the statement's pending catalog journal.
	PendingCatalog() *session.Journal
	EngineStorageLocked() *storage.Store
	// RoutineAuthorizationLocked is the operation's current authorization,
	// as the calling routine sees it.
	RoutineAuthorizationLocked() (A, error)
	// ResolveRelationLocked resolves schema.name for a utility.
	ResolveRelationLocked(auth A, schema, name string) (ids.ObjectRef, error)
	// VacuumAllowedLocked reports whether auth may maintain relation.
	VacuumAllowedLocked(auth A, relation *catalog.Relation) (bool, error)
	// AcquireObjectLock takes a statement lock on ref, reserving the
	// replacement read.
	AcquireObjectLock(ref ids.ObjectRef, mode lock.Mode) error
	// PublishedRelationLocked is the relation in the published catalog.
	PublishedRelationLocked(address ids.ObjectAddress) (*catalog.Relation, bool)
	// StatementCatalogLocked is the statement's private catalog journal,
	// created on first write; imports record into it.
	StatementCatalogLocked() *session.Journal
}

// Authority is the operation's authorization, held by value: the import is
// generic over it, so capturing it never allocates.
type Authority interface {
	Access() access.View
}

// CatalogAuthority is the catalog runtime's authorization: its view and the
// pending journal whose own uncommitted statistics the visitors resolve.
type CatalogAuthority interface {
	Authority
	PendingCatalog() *session.Journal
}

// RecipeAuthority admits the routines of a statistics expression recipe.
type RecipeAuthority interface {
	exec.AdmissionAuthority
	Access() access.View
}

// SnapshotAuthority is the captured bootstrap authorization a snapshot codec
// binds and evaluates constructor values under.
type SnapshotAuthority interface {
	RecipeAuthority
	analyze.Authority
}

// SnapshotHost is the captured destination authority of a snapshot codec.
// Core implements it over its bootstrap authorization view.
type SnapshotHost interface {
	// Authority is a fresh copy of the authorization for one decode: the
	// binder and admission may recapture it.
	Authority() SnapshotAuthority
	// ControlLiteralAuthority is the explicit private codec evaluation origin;
	// it never replaces the binder's genuine concrete captured authority.
	ControlLiteralAuthority() exec.ControlLiteralAuthority
	// BindingHost is the binder host for one constructor scalar: type,
	// namespace and object-name hooks over the captured authority.
	BindingHost(ctx context.Context) analyze.Host
}

func sqlError(code, message string) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: code, Message: message,
	}
}
