package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type inheritancePreparedAction struct {
	old, next *catalog.Relation
	added     table.AddedColumn
	drop      *preparedDrop
	identity  *preparedIdentityDrop
	types     []catalog.Descriptor
	using     *index.UsingIndexPreparation
	err       error
	notices   []sqlstate.Error
}
type inheritancePreparedNode struct {
	old                                            *catalog.Relation
	actions                                        []parser.AlterTableAction
	graph                                          *catalog.TableInheritance
	steps                                          map[int]inheritancePreparedAction
	inline                                         []inheritancePreparedAction
	checks                                         table.PreparedAddedChecks
	afterDrop                                      *preparedDrop
	reader                                         table.InheritanceDefinitionReader
	phaseZeroOnly                                  bool
	usingIndexes                                   []index.UsingIndexAttachment
	usingNotNull                                   []inheritancePreparedAction
	afterDropOld, usingCaptureOld                  *catalog.Relation
	afterDropError, usingCaptureError, checksError error
}

func (e *Engine) prepareInheritanceNode(scope *StatementScope, op *session.Operation, auth AuthorizationView, old *catalog.Relation, actions []parser.AlterTableAction) (*inheritancePreparedNode, error) {
	state := scope.inheritance.alter
	prepared := &inheritancePreparedNode{old: old, actions: actions, graph: state.Graph, steps: make(map[int]inheritancePreparedAction)}
	prepared.phaseZeroOnly = state.Only && !table.InheritancePureDefinitionPreparation(actions)
	reader := state.Definition
	reader.Observations = &table.InheritanceDefinitionObservations{}
	previousDefinition := op.Definition
	defer func() { op.Definition = previousDefinition }()
	next := old
	var additions []table.AddedColumn
	var prior *preparedDrop
	attachmentIndex := 0
phases:
	for phase := 0; phase <= 6; phase++ {
		for index, action := range actions {
			if table.AlterActionPhase(action.Action) != phase {
				continue
			}
			if err := op.Context().Err(); err != nil {
				return nil, err
			}
			if action.Action == parser.AddUsingIndex {
				captured := prepared.usingIndexes[attachmentIndex]
				attachmentIndex++
				actual, err := e.prepareUsingIndexMetadata(op, auth.Catalog, reader, next, captured)
				if err != nil {
					return nil, err
				}
				prepared.steps[index] = inheritancePreparedAction{old: next, next: actual.Next, using: actual}
				if actual.Err != nil {
					break phases
				}
				next = actual.Next
				reader.AppendDescriptor(actual.Replacement)
				reader.AppendDescriptor(next)
				continue
			}
			op.Definition = reader
			auth.definition = reader
			step := inheritancePreparedAction{old: next}
			beforeReader := reader
			previousNotices := op.Notices
			op.Notices = nil
			var err error
			switch action.Action {
			case parser.DropColumn:
				step.drop, err = scope.prepareColumnDropAfter(auth, next, scope.lockedRef, action, prior)
				if err == nil {
					next = step.drop.next
					prior = step.drop
					reader.AppendDelta(step.drop)
				}
			case parser.AddColumn:
				next, step.added, err = e.addColumnCandidate(scope, op, auth, next, action, nil)
				if err == nil && step.added.Column != nil {
					additions = append(additions, step.added)
				}
			case parser.SetNotNull, parser.AddNotNull, parser.DropNotNull, parser.DropConstraint:
				next, err = table.NotNullCandidate(op, auth.Catalog, next, action, scope.inheritanceNotNullMode(action.Column.Text))
			case parser.DropIdentity:
				step.identity, err = e.prepareIdentityDropDefinition(scope, auth, next, action)
				if err == nil {
					next = step.identity.next
					reader.AppendDelta(step.identity)
				}
			default:
				// These providers require preparatory all-closure grants and therefore
				// are cooked once in the ordinary sole-stage path, not in prefix admission.
				err = fmt.Errorf("stuntgres: unexpected prefix definition provider")
			}
			step.notices = op.DetachNotices()
			op.Notices = previousNotices
			if err == nil && action.Action == parser.AddNotNull {
				err = scope.inheritanceOnlyCreatedConstraint(step.old, next)
			}
			if err == nil {
				step.next = next
				reader.AppendDescriptor(next)
				step.types, err = table.PrepareInheritanceRowType(&reader, next)
			}
			if err != nil {
				step.err = err
				step.next = step.old
				prepared.steps[index] = step
				next = step.old
				reader = beforeReader
				break phases
			}
			prepared.steps[index] = step
			scope.recordInheritanceAlterAction(step.old, next, action)
		}
		if phase == 0 {
			op.Definition = reader
			auth.definition = reader
			var err error
			prepared.afterDropOld = next
			prepared.afterDrop, err = scope.prepareConstraintIndexDrop(auth, old, next, actions, prior)
			if err != nil {
				prepared.afterDropError = err
				break phases
			}
			if prepared.afterDrop != nil {
				next = prepared.afterDrop.next
				prior = prepared.afterDrop
				reader.AppendDelta(prepared.afterDrop)
			}
		}
		if prepared.phaseZeroOnly {
			break
		}
		if phase == 1 {
			var err error
			prepared.usingCaptureOld = next
			prepared.usingIndexes, err = index.CaptureUsingIndexDefinitions(reader, next, actions)
			if err != nil {
				prepared.usingCaptureError = err
				break phases
			}
		}
		if phase == 4 {
			for _, added := range additions {
				if !added.Declaration.ColumnNotNull {
					continue
				}
				action := parser.AlterTableAction{Action: parser.AddNotNull, Column: parser.Identifier{Text: added.Column.Name}, Constraint: parser.Identifier{Text: added.Declaration.ColumnName}}
				before := next
				var err error
				next, err = table.NotNullCandidate(op, auth.Catalog, next, action, table.NotNullDirect)
				prepared.inline = append(prepared.inline, inheritancePreparedAction{old: before, next: next, err: err})
				if err != nil {
					next = before
					break phases
				}
				reader.AppendDescriptor(next)
				scope.recordInheritanceAlterAction(before, next, action)
			}
			for i, captured := range prepared.usingIndexes {
				before := next
				var err error
				next, err = table.UsingIndexNotNullCandidate(op, auth.Catalog, reader, next, captured)
				if err == nil {
					err = scope.inheritanceOnlyCreatedConstraint(before, next)
				}
				prepared.usingNotNull = append(prepared.usingNotNull, inheritancePreparedAction{old: before, next: next, err: err})
				if err != nil {
					next = before
					break phases
				}
				prepared.usingIndexes[i].NotNullPrepared = true
				reader.AppendDescriptor(next)
				scope.recordInheritanceAlterAction(before, next, parser.AlterTableAction{Action: parser.AddNotNull})
			}
		}
		if phase == 6 {
			var raw []*parser.CheckConstraint
			for _, action := range actions {
				if action.Action == parser.AddCheck {
					raw = append(raw, action.Check)
				}
			}
			for _, added := range additions {
				for _, constraint := range added.Column.Constraints {
					if constraint.Check != nil {
						raw = append(raw, constraint.Check)
					}
				}
			}
			if len(raw) > 1 {
				slices.SortFunc(raw, func(a, b *parser.CheckConstraint) int { return a.Pos.Offset - b.Pos.Offset })
			}
			op.Definition = reader
			auth.definition = reader
			var err error
			prepared.checks, err = e.prepareAddedChecks(scope, op, auth, next, raw)
			if err != nil {
				prepared.checks = table.PreparedAddedChecks{Old: next}
				prepared.checksError = err
				break phases
			}
			if len(raw) != 0 {
				scope.recordInheritanceChecks(next, prepared.checks.Next, raw, prepared.checks.Ordinal)
				next = prepared.checks.Next
				reader.AppendDescriptor(next)
			}
		}
	}
	prepared.reader = reader
	state.Definition = reader
	return prepared, nil
}

func (scope *StatementScope) inheritancePreparedNode() *inheritancePreparedNode {
	if scope.inheritance.alter == nil {
		return nil
	}
	return scope.inheritance.alter.prepared[scope.lockedRef.Address]
}

func (scope *StatementScope) consumeInheritancePreparedAction(stage *CatalogStage, prepared *inheritancePreparedNode, idx int, old *catalog.Relation, ref ids.ObjectRef, changes *session.Journal) (*catalog.Relation, table.AddedColumn, bool, error) {
	if prepared == nil {
		return nil, table.AddedColumn{}, false, nil
	}
	step, exists := prepared.steps[idx]
	if !exists {
		return nil, table.AddedColumn{}, false, nil
	}
	if step.old != old {
		return nil, table.AddedColumn{}, true, ids.ErrCatalogReplaced
	}
	if len(step.notices) != 0 {
		scope.op.Notices = append(scope.op.Notices, step.notices...)
	}
	if step.err != nil {
		return nil, table.AddedColumn{}, true, step.err
	}
	for _, descriptor := range step.types {
		if err := stage.replaceDescriptor(descriptor, false); err != nil {
			return nil, table.AddedColumn{}, true, err
		}
	}
	if step.drop != nil {
		next, err := scope.consumePreparedDrop(stage, step.drop, old, ref, changes)
		return next, step.added, true, err
	}
	if step.identity != nil {
		next, err := scope.consumeIdentityDropDefinition(stage, step.identity, old, ref)
		return next, step.added, true, err
	}
	if step.using != nil {
		next, err := index.StagePreparedUsingIndex(scope.op, stage, old, step.using)
		return next, step.added, true, err
	}
	return step.next, step.added, true, nil
}
