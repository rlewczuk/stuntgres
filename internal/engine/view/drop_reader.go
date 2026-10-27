package view

import (
	"context"
	"iter"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// DropReader is the drop stage the DROP read side decides over: the base
// catalog the pass started from, the staged or prepared replacements and
// removals, and the SQL lookup path for diagnostics. The root dropProvider
// implements it, as it implements table.DropDependencyReader.
type DropReader interface {
	Context() context.Context
	BaseRead() catalog.Read
	// Path is the SQL lookup path that qualifies diagnostic names; nil for an
	// internal provider stage, which never qualifies.
	Path() []ids.ObjectAddress
	// Descriptor reads address through the pass, recording it as observed.
	Descriptor(ids.ObjectAddress) catalog.Descriptor
	// Descriptors enumerates the base and the pass's new descriptors.
	Descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor]
	DescriptorCount() int
	// InputDescriptors enumerates the descriptors entering the pass.
	InputDescriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor]
	// UnseededDescriptors and UnseededInputDescriptors may omit the base
	// descriptors that are still as a builtin seed installed them. Those
	// reference no object for which SeedMayReference is false.
	UnseededDescriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor]
	UnseededInputDescriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor]
	SeedMayReference(ids.ObjectAddress) bool
	// Relations and InputRelations may omit the non-relation descriptors of
	// Descriptors and InputDescriptors.
	Relations() iter.Seq2[ids.ObjectAddress, catalog.Descriptor]
	InputRelations() iter.Seq2[ids.ObjectAddress, catalog.Descriptor]
	// RetirementDescriptor reads address even after the pass removed it, so
	// a retired provider's automatic closure stays traversable.
	RetirementDescriptor(ids.ObjectAddress) catalog.Descriptor
	Replacement(ids.ObjectAddress) catalog.Descriptor
	Removed(ids.ObjectAddress) bool
	Replace(catalog.Descriptor) error
	Revision(ids.DefinitionRevision) (ids.DefinitionRevision, error)
	// DefinitionInput is the definition the pass reads SQL dependencies
	// through: the stage or the preparation; nil when it has neither.
	DefinitionInput() catalog.DefinitionReader
}
