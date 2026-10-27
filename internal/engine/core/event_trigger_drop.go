package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Record the exact successful logical DROP step, before a later ADD can reuse
// its name. This is neither a command-tag guess nor a whole-catalog diff.
func (s *StatementScope) RecordAlterDeletion(auth AuthorizationView, before, next *catalog.Relation, action parser.AlterTableAction) {
	if s.EventCommand == nil || before == nil || next == nil {
		return
	}
	switch action.Action {
	case parser.DropColumn, parser.DropConstraint, parser.DropNotNull, parser.DropExpression, parser.SetExpression:
		p, err := s.alterDropProvider(auth)
		if err != nil {
			s.EventCommand.captureError = err
			return
		}
		if p == nil {
			return
		}
		records, err := p.CaptureRelationProjection(before, next)
		if err != nil {
			s.EventCommand.captureError = err
			return
		}
		s.RecordDroppedObjects(records)
	}
	switch action.Action {
	case parser.DropExpression, parser.SetExpression:
		if slot, exists := before.Index[action.Column.Text]; exists {
			if generation, present := before.ColumnMeta.Generations[before.Attributes[slot]]; present {
				s.RecordRelationDeletion(auth, before, ids.ObjectAddress{ClassID: 2604, ObjectID: generation.OID})
			}
		}
	case parser.DropColumn:
		if slot, exists := before.Index[action.Column.Text]; exists && !next.HasAttribute(before.Attributes[slot]) {
			address := before.Address()
			address.SubID = before.Attributes[slot]
			s.RecordRelationDeletion(auth, before, address)
		}
	case parser.DropConstraint:
		if catalog.RelationConstraintNameExists(next, action.Constraint.Text) {
			return
		}
		address, _ := table.DroppedConstraint(before, action)
		if address != (ids.ObjectAddress{}) {
			s.RecordRelationDeletion(auth, before, address)
			return
		}
		for _, constraint := range before.Constraints.ForeignKeys {
			if constraint.Name == action.Constraint.Text {
				s.RecordRelationDeletion(auth, before, ids.ObjectAddress{ClassID: 2606, ObjectID: constraint.OID})
				return
			}
		}
	case parser.DropNotNull:
		slot, exists := before.Index[action.Column.Text]
		if !exists {
			return
		}
		for _, constraint := range before.Constraints.NotNull {
			if constraint.Attribute == before.Attributes[slot] && !catalog.RelationConstraintNameExists(next, constraint.Name) {
				s.RecordRelationDeletion(auth, before, ids.ObjectAddress{ClassID: 2606, ObjectID: constraint.OID})
			}
		}
	}
}
