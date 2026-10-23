package role

import (
	"context"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Host is what the role decisions made outside a catalog stage need from the
// engine for one statement: GRANT targets, DROP ROLE dependencies and ALTER
// OWNER checks. Every method runs with Engine.mu held, never reads a catalog
// stage and never runs a provider callback.
type Host interface {
	Context() context.Context
	// AccessView returns the catalog, roles, state and owner of auth.
	AccessView(ddl.Authority) access.View
	// SearchNamespaces returns the namespaces a name of class searches, in
	// order (see ddl.Host).
	SearchNamespaces(auth ddl.Authority, name parser.ObjectName, class ids.OID) ([]ids.ObjectAddress, error)

	// ResolveRelation resolves a relation named by GRANT, missing ones
	// included in the error.
	ResolveRelation(auth ddl.Authority, name parser.ObjectName) (ids.ObjectRef, error)
	// ArgumentTypes captures regtype input once for auth and returns the
	// resolver of routine argument types it binds to the operation.
	ArgumentTypes(auth ddl.Authority) (func(context.Context, parser.TypeName) (uint32, error), error)
	// CanonicalParameter folds a GRANT ON PARAMETER name to its setting key.
	CanonicalParameter(name string) (string, error)
	// GrantParameter returns the parameter's ACL identity, or an unpublished
	// baseline identity when it has none.
	GrantParameter(auth ddl.Authority, key string) (*catalog.Parameter, error)
	// ReferencingFeatures names the registered features that reference role.
	ReferencingFeatures(role authorization.PrincipalRef) []string
	// OtherDatabaseDependencies captures local-object dependencies in other
	// registered databases; shared database descriptors are already in Catalog.
	OtherDatabaseDependencies(role authorization.PrincipalRef) []access.RoleDependency
	// DescribeRoleDependency renders one DROP ROLE dependency detail line.
	DescribeRoleDependency(auth ddl.Authority, dep access.RoleDependency) string
}

// StageHost is what REASSIGN OWNED and DROP OWNED staging needs: the shared
// DDL host over the statement's stage, its observed dependencies, object
// removal and the stage as an OwnedCleanupStage.
type StageHost interface {
	ddl.Host

	// AddDomainBase records d among the stage's observed dependencies.
	AddDomainBase(d catalog.Descriptor)
	// RemoveObject stages the removal of ref.
	RemoveObject(ref ids.ObjectRef, namespace bool) error
	// OwnerStage is the statement's stage.
	OwnerStage() OwnedCleanupStage
}
