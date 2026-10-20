package extension

import (
	"context"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// Authority is the statement authority the handlers check privileges and
// ownership with. The engine's statement authorization view implements it;
// the catalog, roles and role state it captured are passed beside it as an
// access.View.
type Authority interface {
	Check(access.Requirement) error
	Effective(ref ids.ObjectRef, grantOptions bool) (authorization.Privileges, error)
}

// Host is what the CREATE EXTENSION and ALTER EXTENSION UPDATE handlers need
// from the engine for one statement. Every method is called with the
// engine's catalog mutex held and never takes or releases it.
type Host interface {
	Context() context.Context
	// Notice emits a NOTICE-level message to the statement's session.
	Notice(sqlstate.Error)
	// CreationPath is the explicit search path; its first entry is the
	// default creation namespace.
	CreationPath() ([]ids.ObjectAddress, error)
	// ReplayingTableDestination reports a trusted Restore replay, which
	// skips the trusted-extension and CREATE privilege checks.
	ReplayingTableDestination() bool
	// BootstrapSuperuser owns the members of a superuser-only extension
	// that a non-superuser installs.
	BootstrapSuperuser() authorization.PrincipalRef
	AllocateOID() (ids.OID, error)
	Revision() (ids.DefinitionRevision, error)
	// BeginStage begins the statement's catalog stage from the captured
	// catalog, authorizes it and records namespace as the extension
	// namespace its publication observes.
	BeginStage(namespace ids.ObjectAddress) Stage
}
