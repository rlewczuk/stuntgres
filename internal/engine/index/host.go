package index

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// KeyHost is what binding an index key and looking up an operator class
// need: the authority's catalog, the operator search path and collation
// lookup. It reads no stage, so the analysis host and partition key binding
// pass a host without one. Every method runs with Engine.mu held and never
// waits.
type KeyHost interface {
	// AccessView returns the catalog, roles, state and owner of auth.
	AccessView(auth ddl.Authority) access.View
	// SearchNamespaces returns the namespaces a name of class searches.
	SearchNamespaces(auth ddl.Authority, name parser.ObjectName, class ids.OID) ([]ids.ObjectAddress, error)
	// LookupCollation resolves a COLLATE name with the operation's authority,
	// not the statement's: 42704 when it does not exist, 0A000 when it is
	// unsupported.
	LookupCollation(raw parser.ObjectName) (uint32, error)
}

// Host is what CREATE INDEX, ALTER/DROP INDEX, REINDEX and constraint-index
// staging need from the engine for one statement: the shared DDL host plus
// the index stage slot, descriptor staging, ACL output, the stage's
// definition reader, collation lookup and expression binding. The engine
// builds binders; this package never does.
//
// Every method runs with Engine.mu held, never takes or releases it, and
// never waits.
type Host interface {
	ddl.Host
	KeyHost

	// AddDomainBase records d among the stage's observed dependencies, so a
	// concurrent change to it rejects the publication.
	AddDomainBase(d catalog.Descriptor)
	// RemoveObject stages the removal of ref.
	RemoveObject(ref ids.ObjectRef, namespace bool) error
	// TrustedRestoreDestination reports whether the statement restores into
	// a trusted table destination, and fails when that Restore lacks its
	// bootstrap superuser authority ("invalid trusted restore <what>
	// authority").
	TrustedRestoreDestination(auth ddl.Authority, what string) (bool, error)
	// RelationDiagnostic returns the kind and name a relation has in
	// diagnostics.
	RelationDiagnostic(auth ddl.Authority, relation *catalog.Relation) (kind, name string)

	// IndexStage is the stage's index slot, allocated on first use.
	IndexStage() *Stage
	// DescriptorStage is the stage itself, for partition descriptors,
	// backing-index renames and USING INDEX attachment.
	DescriptorStage() DescriptorStage
	// ACLOutput renders aclitem values for stored expressions read through
	// view, with the operation's roles.
	ACLOutput(view catalog.Read) regobj.ACLItemOutput
	// DefinitionReader reads definitions through the stage.
	DefinitionReader() catalog.DefinitionReader

	// BindIndexExpression binds, preflights and renders one index key
	// expression of parent, and returns its column label.
	BindIndexExpression(auth ddl.Authority, parent *catalog.Relation, raw parser.Expr, literals []plan.LiteralExpr, input sqltype.InputEnvironment) (*catalog.DerivedIndexKey, string, error)
	// BindIndexPredicate binds, preflights and renders a partial index
	// predicate of parent; nil when raw is nil or the constant TRUE.
	BindIndexPredicate(auth ddl.Authority, parent *catalog.Relation, raw parser.Expr, input sqltype.InputEnvironment) (*catalog.IndexPredicate, error)
}

// DropStage is what DROP INDEX staging needs. It runs without Engine.mu;
// RemoveRelation takes it.
type DropStage interface {
	DescriptorStage
	// StageRead reads the stage over its base.
	StageRead() catalog.Read
	// RemoveRelation stages the removal of the relation at address.
	RemoveRelation(address ids.ObjectAddress) error
}

// ValidityTarget is the drop provider the partitioned-index validity pass
// reads and updates.
type ValidityTarget interface {
	CoverageReader
	// DescriptorCount bounds the passes over the hierarchy.
	DescriptorCount() int
	// ReplacePartitionDescriptor stages a revised partitioned-index
	// descriptor.
	ReplacePartitionDescriptor(d *catalog.Relation) error
}
