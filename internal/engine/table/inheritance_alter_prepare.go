package table

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func PrepareInheritanceRowType(reader *InheritanceDefinitionReader, relation *catalog.Relation) ([]catalog.Descriptor, error) {
	if relation.RowType == 0 {
		return nil, nil
	}
	descriptor, _ := reader.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)})
	row, ok := descriptor.(*catalog.Type)
	if !ok {
		return nil, nil
	}
	var effects []catalog.Descriptor
	err := typedef.SynchronizeRowTypeMetadata(relation, row, func(address ids.ObjectAddress) catalog.Descriptor {
		descriptor, _ := reader.Descriptor(address)
		return descriptor
	}, func(namespace ids.ObjectAddress, name string) string {
		return typedef.ChooseAssociatedArrayName(name, func(candidate string) bool {
			return reader.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: candidate}) != (ids.ObjectAddress{})
		})
	}, func(descriptor catalog.Descriptor) error {
		reader.AppendDescriptor(descriptor)
		effects = append(effects, descriptor)
		return nil
	})
	return effects, err
}

func DetachPreparedInheritanceDefinition(next *catalog.Relation) *catalog.Relation {
	copy := *next
	copy.Constraints.NotNull = slices.Clone(next.Constraints.NotNull)
	copy.Constraints.Check = slices.Clone(next.Constraints.Check)
	return &copy
}

// InheritancePreparedAction is one prepared action of an inheritance node:
// the candidate it leaves, or the error the ALTER loop reports on re-entry.
type InheritancePreparedAction struct {
	Old, Next *catalog.Relation
	Added     AddedColumn
	Drop      PreparedDrop
	Identity  PreparedDrop
	Types     []catalog.Descriptor
	Using     *index.UsingIndexPreparation
	Err       error
	Notices   []sqlstate.Error
}

// InheritancePreparedNode is one inheritance node's prepared definition
// candidates, replayed in phase order by the ALTER loop on the sole stage.
type InheritancePreparedNode struct {
	Old                                            *catalog.Relation
	Actions                                        []parser.AlterTableAction
	Graph                                          *catalog.TableInheritance
	Steps                                          map[int]InheritancePreparedAction
	Inline                                         []InheritancePreparedAction
	Checks                                         PreparedAddedChecks
	AfterDrop                                      PreparedDrop
	Reader                                         InheritanceDefinitionReader
	PhaseZeroOnly                                  bool
	UsingIndexes                                   []index.UsingIndexAttachment
	UsingNotNull                                   []InheritancePreparedAction
	AfterDropOld, UsingCaptureOld                  *catalog.Relation
	AfterDropError, UsingCaptureError, ChecksError error
}

// PrepareInheritanceNode cooks phases 0-6 of one inheritance node's actions
// before the sole stage exists, through h, over view and the recursing
// plan, whose definition reader it extends. It sets op.Definition for each
// provider call and restores it on return. Callers hold nothing; h's
// methods take Engine.mu and may wait.
func PrepareInheritanceNode(h PrepareHost, op *session.Operation, view catalog.Read, plan *InheritanceAlterPlan, old *catalog.Relation, actions []parser.AlterTableAction) (*InheritancePreparedNode, error) {
	prepared := &InheritancePreparedNode{Old: old, Actions: actions, Graph: plan.Graph, Steps: make(map[int]InheritancePreparedAction)}
	prepared.PhaseZeroOnly = plan.Only && !InheritancePureDefinitionPreparation(actions)
	reader := plan.Definition
	reader.Observations = &InheritanceDefinitionObservations{}
	previousDefinition := op.Definition
	defer func() { op.Definition = previousDefinition }()
	next := old
	var additions []AddedColumn
	var prior PreparedDrop
	attachmentIndex := 0
phases:
	for phase := 0; phase <= 6; phase++ {
		for index, action := range actions {
			if AlterActionPhase(action.Action) != phase {
				continue
			}
			if err := op.Context().Err(); err != nil {
				return nil, err
			}
			if action.Action == parser.AddUsingIndex {
				captured := prepared.UsingIndexes[attachmentIndex]
				attachmentIndex++
				actual, err := h.PrepareUsingIndexMetadata(reader, next, captured)
				if err != nil {
					return nil, err
				}
				prepared.Steps[index] = InheritancePreparedAction{Old: next, Next: actual.Next, Using: actual}
				if actual.Err != nil {
					break phases
				}
				next = actual.Next
				reader.AppendDescriptor(actual.Replacement)
				reader.AppendDescriptor(next)
				continue
			}
			op.Definition = reader
			step := InheritancePreparedAction{Old: next}
			beforeReader := reader
			previousNotices := op.Notices
			op.Notices = nil
			var err error
			switch action.Action {
			case parser.DropColumn:
				step.Drop, err = h.PrepareColumnDropAfter(reader, next, action, prior)
				if err == nil {
					next = step.Drop.Next()
					prior = step.Drop
					reader.AppendDelta(step.Drop)
				}
			case parser.AddColumn:
				next, step.Added, err = h.AddColumnCandidate(reader, next, action)
				if err == nil && step.Added.Column != nil {
					additions = append(additions, step.Added)
				}
			case parser.SetNotNull, parser.AddNotNull, parser.DropNotNull, parser.DropConstraint:
				next, err = NotNullCandidate(op, view, next, action, plan.NotNullMode(action.Column.Text))
			case parser.DropIdentity:
				step.Identity, err = h.PrepareIdentityDropDefinition(reader, next, action)
				if err == nil {
					next = step.Identity.Next()
					reader.AppendDelta(step.Identity)
				}
			default:
				// These providers require preparatory all-closure grants and therefore
				// are cooked once in the ordinary sole-stage path, not in prefix admission.
				err = fmt.Errorf("stuntgres: unexpected prefix definition provider")
			}
			step.Notices = op.DetachNotices()
			op.Notices = previousNotices
			if err == nil && action.Action == parser.AddNotNull {
				err = plan.OnlyCreatedConstraint(step.Old, next)
			}
			if err == nil {
				step.Next = next
				reader.AppendDescriptor(next)
				step.Types, err = PrepareInheritanceRowType(&reader, next)
			}
			if err != nil {
				step.Err = err
				step.Next = step.Old
				prepared.Steps[index] = step
				next = step.Old
				reader = beforeReader
				break phases
			}
			prepared.Steps[index] = step
			plan.RecordAction(step.Old, next, action)
		}
		if phase == 0 {
			op.Definition = reader
			var err error
			prepared.AfterDropOld = next
			prepared.AfterDrop, err = h.PrepareConstraintIndexDrop(reader, old, next, actions, prior)
			if err != nil {
				prepared.AfterDropError = err
				break phases
			}
			if prepared.AfterDrop != nil {
				next = prepared.AfterDrop.Next()
				prior = prepared.AfterDrop
				reader.AppendDelta(prepared.AfterDrop)
			}
		}
		if prepared.PhaseZeroOnly {
			break
		}
		if phase == 1 {
			var err error
			prepared.UsingCaptureOld = next
			prepared.UsingIndexes, err = index.CaptureUsingIndexDefinitions(reader, next, actions)
			if err != nil {
				prepared.UsingCaptureError = err
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
				next, err = NotNullCandidate(op, view, next, action, NotNullDirect)
				prepared.Inline = append(prepared.Inline, InheritancePreparedAction{Old: before, Next: next, Err: err})
				if err != nil {
					next = before
					break phases
				}
				reader.AppendDescriptor(next)
				plan.RecordAction(before, next, action)
			}
			for i, captured := range prepared.UsingIndexes {
				before := next
				var err error
				next, err = UsingIndexNotNullCandidate(op, view, reader, next, captured)
				if err == nil {
					err = plan.OnlyCreatedConstraint(before, next)
				}
				prepared.UsingNotNull = append(prepared.UsingNotNull, InheritancePreparedAction{Old: before, Next: next, Err: err})
				if err != nil {
					next = before
					break phases
				}
				prepared.UsingIndexes[i].NotNullPrepared = true
				reader.AppendDescriptor(next)
				plan.RecordAction(before, next, parser.AlterTableAction{Action: parser.AddNotNull})
			}
		}
		if phase == 6 {
			raw := AddedCheckSyntax(actions, additions)
			op.Definition = reader
			var err error
			prepared.Checks, err = h.PrepareAddedChecks(reader, next, raw)
			if err != nil {
				prepared.Checks = PreparedAddedChecks{Old: next}
				prepared.ChecksError = err
				break phases
			}
			if len(raw) != 0 {
				plan.RecordChecks(next, prepared.Checks.Next, raw, prepared.Checks.Ordinal)
				next = prepared.Checks.Next
				reader.AppendDescriptor(next)
			}
		}
	}
	prepared.Reader = reader
	plan.Definition = reader
	return prepared, nil
}
