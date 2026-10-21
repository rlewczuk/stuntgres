package table

import (
	"context"
	"iter"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// dropDependencyReader is what the column, CHECK, DEFAULT and foreign-key
// dependency passes need from the drop pipeline: the input catalog with this
// drop's replacements and removals, staging of replaced descriptors, and the
// passes that stay with the pipeline. dropProvider implements it.
type DropDependencyReader interface {
	Context() context.Context
	BaseRead() catalog.Read
	InputDescriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor]
	Descriptor(ids.ObjectAddress) catalog.Descriptor
	Replacement(ids.ObjectAddress) catalog.Descriptor
	Removed(ids.ObjectAddress) bool
	Replace(catalog.Descriptor) error
	Revision(ids.DefinitionRevision) (ids.DefinitionRevision, error)
	InheritanceAlterGuard(*catalog.Relation) error
	// baseRef names address in the domain at the base epoch.
	BaseRef(ids.ObjectAddress) ids.ObjectRef
	// statementBound reports whether a statement runs the drop (not a
	// stage-only dependency query).
	StatementBound() bool
	// recursingInheritance reports whether the statement is an inheritance
	// ALTER TABLE recursion.
	RecursingInheritance() bool
	DependencyClosure(targets []ids.ObjectAddress) ([]DomainDependency, error)
	Acquire(ref ids.ObjectRef, reserveReplacement bool) error
	ApplyViewDependents(targets []ids.ObjectAddress, mode parser.DropMode, foreign ...DomainDependency) error
	CompositeFieldDependencies(source *catalog.Relation, attribute int32, mode parser.DropMode) error
	OwnedExpressionDependencies(targets []ids.ObjectAddress, mode parser.DropMode) error
	MissingColumnNotice(column, relation string)
}
