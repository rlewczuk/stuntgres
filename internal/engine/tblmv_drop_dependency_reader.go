package engine

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
type dropDependencyReader interface {
	context() context.Context
	baseRead() catalog.Read
	InputDescriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor]
	descriptor(ids.ObjectAddress) catalog.Descriptor
	Replacement(ids.ObjectAddress) catalog.Descriptor
	Removed(ids.ObjectAddress) bool
	Replace(catalog.Descriptor) error
	revision(ids.DefinitionRevision) (ids.DefinitionRevision, error)
	inheritanceAlterGuard(*catalog.Relation) error
	// baseRef names address in the domain at the base epoch.
	baseRef(ids.ObjectAddress) ids.ObjectRef
	// statementBound reports whether a statement runs the drop (not a
	// stage-only dependency query).
	statementBound() bool
	// recursingInheritance reports whether the statement is an inheritance
	// ALTER TABLE recursion.
	recursingInheritance() bool
	dependencyClosure(targets []ids.ObjectAddress) ([]domainDependency, error)
	acquire(ref ids.ObjectRef, reserveReplacement bool) error
	applyViewDependents(targets []ids.ObjectAddress, mode parser.DropMode, foreign ...domainDependency) error
	compositeFieldDependencies(source *catalog.Relation, attribute int32, mode parser.DropMode) error
	ownedExpressionDependencies(targets []ids.ObjectAddress, mode parser.DropMode) error
	missingColumnNotice(column, relation string)
}
