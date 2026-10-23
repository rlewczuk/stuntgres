package catalog

import (
	"iter"

	"stuntgres/internal/engine/ids"
)

// DefinitionReader is the sparse catalog read that definition cooking uses: only
// it can see the exact ordered, unpublished DROP effects of a statement. It is
// not another catalog generation, SQL execution route or permission cache. Bound
// definitions keep their normal identities and provider observations; a reader
// is scoped to preparation and consumption and is never persisted.
// CandidateDefinitions implements it over a candidate; the engine adds a
// catalog stage's index layouts and implements it over prepared drops.
type DefinitionReader interface {
	Descriptor(ids.ObjectAddress) (Descriptor, bool)
	Lookup(ids.NameKey) ids.ObjectAddress
	Descriptors() iter.Seq2[ids.ObjectAddress, Descriptor]
	ProceduresNamed(ids.ObjectAddress, string) iter.Seq[*Procedure]
	HasDynamicProcedureName(string) bool
}
