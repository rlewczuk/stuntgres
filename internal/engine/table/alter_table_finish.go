package table

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// RelationOptionsOnlyCandidate applies an ALTER TABLE whose actions only set
// or reset relation options to old, then checks the tablespace move.
func RelationOptionsOnlyCandidate(old *catalog.Relation, actions []parser.AlterTableAction, destination ids.ObjectRef) (*catalog.Relation, error) {
	next := old
	for _, action := range actions {
		var err error
		next, err = RelationOptionsCandidate(next, action)
		if err != nil {
			return nil, err
		}
	}
	if err := ValidateAlterTablespaceMove(destination); err != nil {
		return nil, err
	}
	return next, nil
}

// AlterNotNullPass is pass 4 of the ALTER TABLE loop on next: the inline NOT
// NULL of each added column, then the NOT NULL each USING INDEX attachment
// needs, which it marks prepared. A recursing statement's prepared node
// (prepared, unless PhaseZeroOnly) supplies the results instead. plan may be
// nil. It takes no lock.
func AlterNotNullPass(op *session.Operation, view catalog.Read, reader catalog.DefinitionReader, plan *InheritanceAlterPlan, prepared *InheritancePreparedNode, next *catalog.Relation, additions []AddedColumn, attachments []index.UsingIndexAttachment) (*catalog.Relation, error) {
	cooked := prepared != nil && !prepared.PhaseZeroOnly
	inlineIndex := 0
	for _, added := range additions {
		if !added.Declaration.ColumnNotNull {
			continue
		}
		before := next
		var err error
		if cooked {
			if inlineIndex >= len(prepared.Inline) || prepared.Inline[inlineIndex].Old != next {
				return nil, ids.ErrCatalogReplaced
			}
			inline := prepared.Inline[inlineIndex]
			inlineIndex++
			if inline.Err != nil {
				return nil, inline.Err
			}
			next = inline.Next
		} else {
			next, err = NotNullCandidate(op, view, next, parser.AlterTableAction{Action: parser.AddNotNull, Column: parser.Identifier{Text: added.Column.Name}, Constraint: parser.Identifier{Text: added.Declaration.ColumnName}}, NotNullDirect)
			if err != nil {
				return nil, err
			}
		}
		plan.RecordAction(before, next, parser.AlterTableAction{Action: parser.AddNotNull})
	}
	for i, captured := range attachments {
		before := next
		var err error
		if cooked {
			using := prepared.UsingNotNull[i]
			if using.Old != next {
				return nil, ids.ErrCatalogReplaced
			}
			if using.Err != nil {
				return nil, using.Err
			}
			next = using.Next
		} else {
			next, err = UsingIndexNotNullCandidate(op, view, reader, next, captured)
			if err == nil {
				err = plan.OnlyCreatedConstraint(before, next)
			}
			if err != nil {
				return nil, err
			}
			attachments[i].NotNullPrepared = true
		}
		plan.RecordAction(before, next, parser.AlterTableAction{Action: parser.AddNotNull})
	}
	return next, nil
}

// AlterFinishInput is the state of an ALTER TABLE after its pass loop.
type AlterFinishInput struct {
	Old, Next *catalog.Relation
	Actions   []parser.AlterTableAction
	// Prepared reports a recursing statement's prepared node.
	Prepared        bool
	Transformations []index.ColumnTransformation
	Replacements    []int32
	// FinishInheritance completes a recursing node's candidate; it runs with
	// nothing held.
	FinishInheritance   func(old, next *catalog.Relation) (*catalog.Relation, error)
	RelationshipOnly    bool
	ForeignKeys         bool
	ValidateConstraints bool
	// StageEmpty reports that the statement staged no descriptor, removal or
	// generator.
	StageEmpty  bool
	Destination ids.ObjectRef
}

// AlterFinish is the finished ALTER TABLE candidate.
type AlterFinish struct {
	Next               *catalog.Relation
	StoredReplacements []int32
	ChangedValues      bool
	// NoOp reports an ALTER TABLE with nothing to stage.
	NoOp bool
}

// FinishAlterCandidate finishes the candidate of the ALTER TABLE pass loop:
// it detaches a prepared inheritance definition, applies the row-security
// flags, finishes the inheritance node and bumps the revision of a changed
// candidate. A relationship-only statement stops before the no-op check.
func FinishAlterCandidate(in AlterFinishInput) (AlterFinish, error) {
	if catalog.HierarchyRelation(in.Old) && HasRelationOptions(in.Actions) && !PartitionConstraintActions(in.Old, in.Actions) {
		return AlterFinish{}, sqlError("0A000", "altering partition hierarchies is unsupported")
	}
	next := in.Next
	if in.Prepared && next != in.Old {
		next = DetachPreparedInheritanceDefinition(next)
	}
	finish := AlterFinish{StoredReplacements: StoredReplacements(next, in.Replacements)}
	finish.ChangedValues = len(in.Transformations) != 0 || len(finish.StoredReplacements) != 0
	next = access.RowSecurityFlags(next, in.Actions)
	next, err := in.FinishInheritance(in.Old, next)
	if err != nil {
		return AlterFinish{}, err
	}
	finish.Next = next
	if in.RelationshipOnly {
		return finish, nil
	}
	if !in.ForeignKeys && !in.ValidateConstraints && len(in.Transformations) == 0 && next == in.Old && in.StageEmpty {
		finish.NoOp = true
		return finish, ValidateAlterTablespaceMove(in.Destination)
	}
	if next != in.Old {
		if err := BumpAlterRevision(in.Old, next); err != nil {
			return AlterFinish{}, err
		}
	}
	return finish, nil
}

// BumpAlterRevision sets next's revision to the one after old's.
func BumpAlterRevision(old, next *catalog.Relation) error {
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next.ObjectRevision = old.Revision() + 1
	return nil
}

// AlterRewrites reports whether ALTER TABLE rewrites the heap and whether it
// rewrites row values: changed values, per-row added defaults or non-null
// added-column fills.
func AlterRewrites(transformations []index.ColumnTransformation, stored []int32, changedValues, perRowAdd bool, fills map[int32]sqltype.Value) (rewrite, rowRewrite bool) {
	rewrite = len(stored) != 0 || perRowAdd
	for _, transformation := range transformations {
		rewrite = rewrite || transformation.Rewrite
	}
	rowRewrite = changedValues || perRowAdd
	for _, fill := range fills {
		rowRewrite = rowRewrite || !fill.IsNull()
	}
	return rewrite, rowRewrite
}

// AlterStorageShape finishes the private storage shape of an ALTER TABLE
// whose final heap is replacement. It reports whether the shape is recorded,
// which requests index reconstruction at publication, and whether the
// statement changed metadata only. Attachment metadata needs no
// reconstruction, so an earlier private shape is kept without restamping it.
func AlterStorageShape(shape session.StorageChange, replacement *storage.Relation, old, next *catalog.Relation, changedValues bool, additions int, physicalRows, attachments bool) (_ session.StorageChange, record, metadataOnly bool) {
	if shape.Replacement != replacement {
		shape.Truncated = false
	}
	shape.Replacement, shape.Relation = replacement, next
	metadataOnly = !changedValues && additions == 0 && slices.Equal(old.Attributes, next.Attributes)
	return shape, physicalRows && (!attachments || !metadataOnly), metadataOnly
}
