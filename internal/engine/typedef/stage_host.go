package typedef

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// StageHost is what the type stage handlers need from the engine for one
// statement: the shared ddl.Host and ddl.TypeStaging plus the type methods
// below. Every method runs with Engine.mu held; none takes or releases it,
// and none waits on the lock manager.
type StageHost interface {
	ddl.Host
	ddl.TypeStaging

	// TrustedRestoreDestination reports whether the statement replays a
	// trusted Restore destination. It fails, naming what, when the replay
	// authority is not the bootstrap superuser of an import.
	TrustedRestoreDestination(auth ddl.Authority, what string) (bool, error)
	AllocateEnumMember() (int64, error)

	// ReplaceUnsynchronized replaces d without re-deriving a row type.
	ReplaceUnsynchronized(catalog.Descriptor) error
	AddDomainBase(catalog.Descriptor)
	RemoveDomainCheck(d *catalog.Type, oid ids.OID) error

	NamedType(ddl.Authority, parser.ObjectName) (*catalog.Type, error)
	FindCollation(ddl.Authority, parser.ObjectName) (*catalog.Collation, error)
	ResolveType(parser.TypeName) (sqltype.Usage, error)
	ResolveDomainBase(parser.TypeName) (sqltype.Usage, error)
	ResolveColumnCollation(sqltype.Usage, *parser.ObjectName) (sqltype.Usage, error)

	BindDomainDefault(auth ddl.Authority, d *catalog.Type, raw parser.Expr, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) error
	BindDomainChecks(auth ddl.Authority, d *catalog.Type, raw []parser.ColumnConstraint, input sqltype.InputEnvironment) error

	NewCompositeRelation(namespace ids.ObjectAddress, name string, owner authorization.PrincipalRef, columns []catalog.Column, index map[string]int) (*catalog.Relation, error)
	StageRowType(*catalog.Relation) error
}
