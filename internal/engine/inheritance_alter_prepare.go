package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type inheritancePreparedAction struct {
	old, next *relationDescriptor
	added     addedColumn
	drop      *preparedDrop
	identity  *preparedIdentityDrop
	types     []Descriptor
	using     *usingIndexPreparation
	err       error
	notices   []sqlstate.Error
}
type inheritancePreparedNode struct {
	old                                            *relationDescriptor
	actions                                        []parser.AlterTableAction
	graph                                          *tableInheritance
	steps                                          map[int]inheritancePreparedAction
	inline                                         []inheritancePreparedAction
	checks                                         preparedAddedChecks
	afterDrop                                      *preparedDrop
	reader                                         inheritanceDefinitionReader
	phaseZeroOnly                                  bool
	usingIndexes                                   []usingIndexAttachment
	usingNotNull                                   []inheritancePreparedAction
	afterDropOld, usingCaptureOld                  *relationDescriptor
	afterDropError, usingCaptureError, checksError error
}

// Both preparation and consumption use the incumbent physical phase ordering.
func alterActionPhase(action parser.AlterTableActionKind) int {
	switch action {
	case parser.SetTablespace:
		// Root destination preparation consumes unchanged placement before phases.
		return -1
	case parser.DropIdentity, parser.DropNotNull, parser.DropConstraint, parser.DropColumn, parser.DropExpression:
		return 0
	case parser.AlterColumnType:
		return 1
	case parser.AddColumn:
		return 2
	case parser.SetExpression:
		return 3
	case parser.SetNotNull:
		return 4
	case parser.AddUnique, parser.AddExclusion, parser.AddUsingIndex:
		return 5
	case parser.AddNotNull:
		return 6
	case parser.AddIdentity:
		return 7
	case parser.SetIdentity:
		return 8
	}
	return -1
}

func (e *Engine) prepareInheritanceNode(scope *StatementScope, op *Operation, auth AuthorizationView, old *relationDescriptor, actions []parser.AlterTableAction) (*inheritancePreparedNode, error) {
	state := scope.inheritanceAlter
	prepared := &inheritancePreparedNode{old: old, actions: actions, graph: state.graph, steps: make(map[int]inheritancePreparedAction)}
	prepared.phaseZeroOnly = state.only && !inheritancePureDefinitionPreparation(actions)
	reader := state.definition
	reader.observations = &inheritanceDefinitionObservations{}
	previousDefinition := op.definition
	defer func() { op.definition = previousDefinition }()
	next := old
	var additions []addedColumn
	var prior *preparedDrop
	attachmentIndex := 0
phases:
	for phase := 0; phase <= 6; phase++ {
		for index, action := range actions {
			if alterActionPhase(action.Action) != phase {
				continue
			}
			if err := op.Context().Err(); err != nil {
				return nil, err
			}
			if action.Action == parser.AddUsingIndex {
				captured := prepared.usingIndexes[attachmentIndex]
				attachmentIndex++
				actual, err := e.prepareUsingIndexMetadata(op, auth.catalog, reader, next, captured)
				if err != nil {
					return nil, err
				}
				prepared.steps[index] = inheritancePreparedAction{old: next, next: actual.next, using: actual}
				if actual.err != nil {
					break phases
				}
				next = actual.next
				reader.appendDescriptor(actual.replacement)
				reader.appendDescriptor(next)
				continue
			}
			op.definition = reader
			auth.definition = reader
			step := inheritancePreparedAction{old: next}
			beforeReader := reader
			previousNotices := op.notices
			op.notices = nil
			var err error
			switch action.Action {
			case parser.DropColumn:
				step.drop, err = scope.prepareColumnDropAfter(auth, next, scope.lockedRef, action, prior)
				if err == nil {
					next = step.drop.next
					prior = step.drop
					reader.appendDrop(step.drop)
				}
			case parser.AddColumn:
				next, step.added, err = e.addColumnCandidate(scope, op, auth, next, action, nil)
				if err == nil && step.added.column != nil {
					additions = append(additions, step.added)
				}
			case parser.SetNotNull, parser.AddNotNull, parser.DropNotNull, parser.DropConstraint:
				next, err = notNullCandidate(op, auth.catalog, next, action, scope.inheritanceNotNullMode(action.Column.Text))
			case parser.DropIdentity:
				step.identity, err = e.prepareIdentityDropDefinition(scope, auth, next, action)
				if err == nil {
					next = step.identity.next
					reader.appendIdentity(step.identity)
				}
			default:
				// These providers require preparatory all-closure grants and therefore
				// are cooked once in the ordinary sole-stage path, not in prefix admission.
				err = fmt.Errorf("stuntgres: unexpected prefix definition provider")
			}
			step.notices = op.DetachNotices()
			op.notices = previousNotices
			if err == nil && action.Action == parser.AddNotNull {
				err = scope.inheritanceOnlyCreatedConstraint(step.old, next)
			}
			if err == nil {
				step.next = next
				reader.appendDescriptor(next)
				step.types, err = prepareInheritanceRowType(&reader, next)
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
			op.definition = reader
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
				reader.appendDrop(prepared.afterDrop)
			}
		}
		if prepared.phaseZeroOnly {
			break
		}
		if phase == 1 {
			var err error
			prepared.usingCaptureOld = next
			prepared.usingIndexes, err = captureUsingIndexDefinitions(reader, next, actions)
			if err != nil {
				prepared.usingCaptureError = err
				break phases
			}
		}
		if phase == 4 {
			for _, added := range additions {
				if !added.declaration.columnNotNull {
					continue
				}
				action := parser.AlterTableAction{Action: parser.AddNotNull, Column: parser.Identifier{Text: added.column.Name}, Constraint: parser.Identifier{Text: added.declaration.columnName}}
				before := next
				var err error
				next, err = notNullCandidate(op, auth.catalog, next, action, notNullDirect)
				prepared.inline = append(prepared.inline, inheritancePreparedAction{old: before, next: next, err: err})
				if err != nil {
					next = before
					break phases
				}
				reader.appendDescriptor(next)
				scope.recordInheritanceAlterAction(before, next, action)
			}
			for i, captured := range prepared.usingIndexes {
				before := next
				var err error
				next, err = usingIndexNotNullCandidate(op, auth.catalog, reader, next, captured)
				if err == nil {
					err = scope.inheritanceOnlyCreatedConstraint(before, next)
				}
				prepared.usingNotNull = append(prepared.usingNotNull, inheritancePreparedAction{old: before, next: next, err: err})
				if err != nil {
					next = before
					break phases
				}
				prepared.usingIndexes[i].notNullPrepared = true
				reader.appendDescriptor(next)
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
				for _, constraint := range added.column.Constraints {
					if constraint.Check != nil {
						raw = append(raw, constraint.Check)
					}
				}
			}
			if len(raw) > 1 {
				slices.SortFunc(raw, func(a, b *parser.CheckConstraint) int { return a.Pos.Offset - b.Pos.Offset })
			}
			op.definition = reader
			auth.definition = reader
			var err error
			prepared.checks, err = e.prepareAddedChecks(scope, op, auth, next, raw)
			if err != nil {
				prepared.checks = preparedAddedChecks{old: next}
				prepared.checksError = err
				break phases
			}
			if len(raw) != 0 {
				scope.recordInheritanceChecks(next, prepared.checks.next, raw, prepared.checks.ordinal)
				next = prepared.checks.next
				reader.appendDescriptor(next)
			}
		}
	}
	prepared.reader = reader
	state.definition = reader
	return prepared, nil
}

func prepareInheritanceRowType(reader *inheritanceDefinitionReader, relation *relationDescriptor) ([]Descriptor, error) {
	if relation.rowType == 0 {
		return nil, nil
	}
	descriptor, _ := reader.descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.rowType)})
	row, ok := descriptor.(*typeDescriptor)
	if !ok {
		return nil, nil
	}
	var effects []Descriptor
	err := synchronizeRowTypeMetadata(relation, row, func(address ids.ObjectAddress) Descriptor {
		descriptor, _ := reader.descriptor(address)
		return descriptor
	}, func(namespace ids.ObjectAddress, name string) string {
		return chooseAssociatedArrayName(name, func(candidate string) bool {
			return reader.lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: candidate}) != (ids.ObjectAddress{})
		})
	}, func(descriptor Descriptor) error {
		reader.appendDescriptor(descriptor)
		effects = append(effects, descriptor)
		return nil
	})
	return effects, err
}

func (scope *StatementScope) inheritancePreparedNode() *inheritancePreparedNode {
	if scope.inheritanceAlter == nil {
		return nil
	}
	return scope.inheritanceAlter.prepared[scope.lockedRef.Address]
}

func (scope *StatementScope) consumeInheritancePreparedAction(stage *CatalogStage, prepared *inheritancePreparedNode, index int, old *relationDescriptor, ref ids.ObjectRef, changes *catalogChanges) (*relationDescriptor, addedColumn, bool, error) {
	if prepared == nil {
		return nil, addedColumn{}, false, nil
	}
	step, exists := prepared.steps[index]
	if !exists {
		return nil, addedColumn{}, false, nil
	}
	if step.old != old {
		return nil, addedColumn{}, true, ids.ErrCatalogReplaced
	}
	if len(step.notices) != 0 {
		scope.op.notices = append(scope.op.notices, step.notices...)
	}
	if step.err != nil {
		return nil, addedColumn{}, true, step.err
	}
	for _, descriptor := range step.types {
		if err := stage.replaceDescriptor(descriptor, false); err != nil {
			return nil, addedColumn{}, true, err
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
		next, err := stagePreparedUsingIndex(scope.op, stage, old, step.using)
		return next, step.added, true, err
	}
	return step.next, step.added, true, nil
}

func detachPreparedInheritanceDefinition(next *relationDescriptor) *relationDescriptor {
	copy := *next
	copy.notNullConstraints = slices.Clone(next.notNullConstraints)
	copy.checkConstraints = slices.Clone(next.checkConstraints)
	return &copy
}
