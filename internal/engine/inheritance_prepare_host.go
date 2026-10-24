package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// inheritancePrepareHost is table.PrepareHost for one inheritance node: the
// recursing statement scope, its operation and the node's authority. Each
// call reads definitions through the reader it is handed, as the
// authority's definition, and runs today's root provider.
type inheritancePrepareHost struct {
	e     *Engine
	scope *StatementScope
	op    *session.Operation
	auth  AuthorizationView
}

var _ table.PrepareHost = inheritancePrepareHost{}

// reading is the node's authority reading definitions through definition.
func (h inheritancePrepareHost) reading(definition catalog.DefinitionReader) AuthorizationView {
	auth := h.auth
	auth.definition = definition
	return auth
}

func (h inheritancePrepareHost) PrepareUsingIndexMetadata(reader catalog.DefinitionReader, parent *catalog.Relation, captured index.UsingIndexAttachment) (*index.UsingIndexPreparation, error) {
	return h.e.prepareUsingIndexMetadata(h.op, h.auth.Catalog, reader, parent, captured)
}

func (h inheritancePrepareHost) AddColumnCandidate(definition catalog.DefinitionReader, old *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, table.AddedColumn, error) {
	return h.e.addColumnCandidate(h.scope, h.op, h.reading(definition), old, action, nil)
}

func (h inheritancePrepareHost) PrepareColumnDropAfter(definition catalog.DefinitionReader, old *catalog.Relation, action parser.AlterTableAction, prior table.PreparedDrop) (table.PreparedDrop, error) {
	drop, err := h.scope.prepareColumnDropAfter(h.reading(definition), old, h.scope.lockedRef, action, preparedDropOf(prior))
	return preparedDropValue(drop), err
}

func (h inheritancePrepareHost) PrepareIdentityDropDefinition(definition catalog.DefinitionReader, old *catalog.Relation, action parser.AlterTableAction) (table.PreparedDrop, error) {
	drop, err := h.e.prepareIdentityDropDefinition(h.scope, h.reading(definition), old, action)
	if drop == nil {
		return nil, err
	}
	return drop, err
}

func (h inheritancePrepareHost) PrepareConstraintIndexDrop(definition catalog.DefinitionReader, old, next *catalog.Relation, actions []parser.AlterTableAction, prior table.PreparedDrop) (table.PreparedDrop, error) {
	drop, err := h.scope.prepareConstraintIndexDrop(h.reading(definition), old, next, actions, preparedDropOf(prior))
	return preparedDropValue(drop), err
}

func (h inheritancePrepareHost) PrepareAddedChecks(definition catalog.DefinitionReader, parent *catalog.Relation, raw []*parser.CheckConstraint) (table.PreparedAddedChecks, error) {
	return h.e.prepareAddedChecks(h.scope, h.op, h.reading(definition), parent, raw)
}

// preparedDropValue boxes a prepared drop; nil stays a nil interface.
func preparedDropValue(drop *table.DropPreparation) table.PreparedDrop {
	if drop == nil {
		return nil
	}
	return drop
}

// preparedDropOf recovers the engine's prepared drop; nil for nil or a
// foreign value, which every consumer rejects as a replaced catalog.
func preparedDropOf(drop table.PreparedDrop) *table.DropPreparation {
	prepared, _ := drop.(*table.DropPreparation)
	return prepared
}

// preparedIdentityDropOf recovers the engine's prepared identity drop, as
// preparedDropOf does.
func preparedIdentityDropOf(drop table.PreparedDrop) *preparedIdentityDrop {
	prepared, _ := drop.(*preparedIdentityDrop)
	return prepared
}
