package table

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/parser"
)

// PreparedDrop is a prepared column, constraint-index or identity drop as the
// inheritance prepare side holds it: the definition effects the reader
// overlays and the relation the drop leaves. The engine builds them; the
// stage consumers recover the engine's own type.
type PreparedDrop interface {
	DefinitionDelta
	Next() *catalog.Relation
}

// PrepareHost is what PrepareInheritanceNode needs from the engine for one
// inheritance node: the providers that cook a definition candidate before
// the statement's sole stage exists. It is bound to one statement scope,
// operation and authority. Each definition argument is the reader the
// authority must read definitions through for that call.
//
// Callers hold nothing; every method takes Engine.mu itself.
// PrepareColumnDropAfter, PrepareIdentityDropDefinition and
// PrepareConstraintIndexDrop may also wait for grants; the others never wait.
type PrepareHost interface {
	// PrepareUsingIndexMetadata prepares one captured ADD ... USING INDEX
	// attachment of parent over reader.
	PrepareUsingIndexMetadata(reader catalog.DefinitionReader, parent *catalog.Relation, captured index.UsingIndexAttachment) (*index.UsingIndexPreparation, error)
	// AddColumnCandidate cooks one ADD COLUMN into a candidate of old.
	AddColumnCandidate(definition catalog.DefinitionReader, old *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, AddedColumn, error)
	// PrepareColumnDropAfter prepares one DROP COLUMN of old after prior,
	// the node's previous prepared drop (nil for none).
	PrepareColumnDropAfter(definition catalog.DefinitionReader, old *catalog.Relation, action parser.AlterTableAction, prior PreparedDrop) (PreparedDrop, error)
	// PrepareIdentityDropDefinition prepares one DROP IDENTITY of old.
	PrepareIdentityDropDefinition(definition catalog.DefinitionReader, old *catalog.Relation, action parser.AlterTableAction) (PreparedDrop, error)
	// PrepareConstraintIndexDrop prepares the drop of the indexes whose
	// constraints actions removed from old, leaving next; nil when there is
	// none.
	PrepareConstraintIndexDrop(definition catalog.DefinitionReader, old, next *catalog.Relation, actions []parser.AlterTableAction, prior PreparedDrop) (PreparedDrop, error)
	// PrepareAddedChecks cooks the added CHECK constraints raw of parent.
	PrepareAddedChecks(definition catalog.DefinitionReader, parent *catalog.Relation, raw []*parser.CheckConstraint) (PreparedAddedChecks, error)
}
