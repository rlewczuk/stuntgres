package table

import (
	"context"
	"iter"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// DropDependencyReader is what the column, CHECK, DEFAULT, foreign-key,
// constraint-index, owned-sequence, composite-field and DROP IDENTITY
// decisions need from the drop pipeline: the input catalog with this drop's
// replacements and removals, staging of replaced descriptors and removals,
// and the passes that stay with the pipeline. The engine's dropProvider
// implements it.
//
// Callers hold nothing. Acquire, CaptureIdentitySequence,
// ColumnDependencies and ExpressionDependencies may wait for locks; Remove and
// ApplyViewDependents take Engine.Mu; the other methods do neither.
type DropDependencyReader interface {
	Context() context.Context
	BaseRead() catalog.Read
	InputDescriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor]
	// Descriptors enumerates the base and the pass's new descriptors.
	Descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor]
	// RemovalDescriptors enumerates what the drop already removed, with the
	// removed descriptors.
	RemovalDescriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor]
	Descriptor(ids.ObjectAddress) catalog.Descriptor
	Replacement(ids.ObjectAddress) catalog.Descriptor
	Removed(ids.ObjectAddress) bool
	Replace(catalog.Descriptor) error
	Revision(ids.DefinitionRevision) (ids.DefinitionRevision, error)
	InheritanceAlterGuard(*catalog.Relation) error
	// BaseRef names address in the domain at the base epoch.
	BaseRef(ids.ObjectAddress) ids.ObjectRef
	// StatementBound reports whether a statement runs the drop (not a
	// stage-only dependency query).
	StatementBound() bool
	// TypedFieldCheckOwner reports whether the admitted typed-table closure
	// of an ALTER TYPE owns relation's inherited CHECKs.
	TypedFieldCheckOwner(*catalog.Relation) bool
	DependencyClosure(targets []ids.ObjectAddress) ([]DomainDependency, error)
	Acquire(ref ids.ObjectRef, reserveReplacement bool) error
	ApplyViewDependents(targets []ids.ObjectAddress, mode parser.DropMode, foreign ...DomainDependency) error
	// Remove removes ref with this drop.
	Remove(ref ids.ObjectRef) error
	// RemoveInternal removes a generated implementation object without sql_drop.
	RemoveInternal(ref ids.ObjectRef) error
	// ExpressionDependencies drops or rejects what depends on roots on behalf
	// of subject.
	ExpressionDependencies(roots []ids.ObjectAddress, mode parser.DropMode, subject ids.ObjectAddress) error
	// ColumnDependencies preflights the full closure of the original live
	// attribute. Its local AUTO payload and physical shape remain column-owned.
	ColumnDependencies(target ids.ObjectAddress, mode parser.DropMode) error
	// Notice emits notice, or records it for a prepared drop.
	Notice(notice sqlstate.Error)
	// CaptureIdentitySequence acquires the identity sequence seq of relation's
	// attribute and captures its definition at the grant.
	CaptureIdentitySequence(relation *catalog.Relation, attribute int32, seq ids.ObjectRef) error
	// TombstoneIdentity forgets the staged identity sequence at address; a
	// prepared drop applies it when consumed.
	TombstoneIdentity(address ids.ObjectAddress)
}
