package index

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Eligibility is captured once from the actual ordered provider input. TYPE
// reminting remains the existing stage owner's separate metadata effect.
func CaptureUsingIndexDefinition(view catalog.Read, reader catalog.DefinitionReader, parent *catalog.Relation, raw *parser.UsingIndexConstraint) (UsingIndexAttachment, error) {
	address := reader.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.Namespace(), Name: raw.Index.Text})
	descriptor, _ := reader.Descriptor(address)
	if descriptor == nil {
		return UsingIndexAttachment{}, expr.ErrorAt("42704", fmt.Sprintf(`index "%s" does not exist`, raw.Index.Text), raw.Pos)
	}
	index, ok := descriptor.(*catalog.Relation)
	if !ok || index.SQLIndex == nil {
		return UsingIndexAttachment{}, sqlError("42809", fmt.Sprintf(`"%s" is not an index`, raw.Index.Text))
	}
	attributes, err := UsingIndexEligibility(view, parent, index, raw)
	if err != nil {
		return UsingIndexAttachment{}, err
	}
	return UsingIndexAttachment{Syntax: raw, Index: index, Attributes: attributes}, nil
}

func CaptureUsingIndexDefinitions(view catalog.Read, reader catalog.DefinitionReader, parent *catalog.Relation, actions []parser.AlterTableAction) ([]UsingIndexAttachment, error) {
	var captured []UsingIndexAttachment
	for _, action := range actions {
		if action.Action == parser.AddUsingIndex {
			attachment, err := CaptureUsingIndexDefinition(view, reader, parent, action.UsingIndex)
			if err != nil {
				return nil, err
			}
			captured = append(captured, attachment)
		}
	}
	return captured, nil
}

type UsingIndexPreparation struct {
	Parent, Next       *catalog.Relation
	Index, Replacement *catalog.Relation
	Notice             sqlstate.Error
	Err                error
}

// IncompatibleNotValid reports a NOT VALID NOT NULL constraint that blocks a
// primary key or a NOT NULL change.
func IncompatibleNotValid(name, table string) error {
	diagnostic := sqlError("55000", fmt.Sprintf(`incompatible NOT VALID constraint "%s" on relation "%s"`, name, table)).(*sqlstate.Error)
	diagnostic.Hint = "You might need to validate it using ALTER TABLE ... VALIDATE CONSTRAINT."
	return diagnostic
}

// Capture the actual physical attachment metadata once; only the stage adapter
// installs its index name/backing link. No tuple/index build runs here.
//
// parent is the table the attachment is staged on, and effective the same
// table with the primary key's NOT NULL constraints prepared (parent itself
// when the capture already prepared them).
func PrepareUsingIndex(a ddl.Allocator, reader catalog.DefinitionReader, parent, effective *catalog.Relation, captured UsingIndexAttachment) (*UsingIndexPreparation, error) {
	prepared := &UsingIndexPreparation{Parent: parent}
	parent = effective
	descriptor, present := reader.Descriptor(captured.Index.Address())
	idx, ok := descriptor.(*catalog.Relation)
	if !present || !ok || idx.SQLIndex == nil {
		return nil, ids.ErrCatalogReplaced
	}
	prepared.Index = idx
	name := captured.Syntax.Name.Text
	if name == "" {
		name = idx.Name()
	}
	if name != idx.Name() {
		prepared.Notice = sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`ALTER TABLE / ADD CONSTRAINT USING INDEX will rename index "%s" to "%s"`, idx.Name(), name)}
	}
	destination := reader.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.Namespace(), Name: name})
	if destination != (ids.ObjectAddress{}) && destination != idx.Address() {
		prepared.Err = sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
		return prepared, nil
	}
	if captured.Syntax.Primary {
		for _, constraint := range parent.Constraints.Unique {
			if constraint.Primary {
				prepared.Err = sqlError("42P16", fmt.Sprintf(`multiple primary keys for table "%s" are not allowed`, parent.Name()))
				return prepared, nil
			}
		}
		if idx.SQLIndex.NullsNotDistinct {
			prepared.Err = sqlError("42P16", "primary keys cannot use NULLS NOT DISTINCT indexes")
			return prepared, nil
		}
		for _, constraint := range parent.Constraints.NotNull {
			if constraint.NotValid && slices.Contains(captured.Attributes, constraint.Attribute) {
				prepared.Err = IncompatibleNotValid(constraint.Name, parent.Name())
				return prepared, nil
			}
		}
	}
	if idx.SQLIndex.Constraint != nil || catalog.RelationConstraintNameExists(parent, name) {
		prepared.Err = UsingIndexCatalogDuplicate(parent, name)
		return prepared, nil
	}
	oid, err := a.AllocateOID()
	if err != nil {
		prepared.Err = err
		return prepared, nil
	}
	constraint := catalog.UniqueConstraint{OID: oid, Name: name, IsLocal: true, Attributes: captured.Attributes, Primary: captured.Syntax.Primary, NullsNotDistinct: idx.SQLIndex.NullsNotDistinct, Index: idx.Address()}
	next := *parent
	next.Constraints.Unique = make([]catalog.UniqueConstraint, len(parent.Constraints.Unique)+1)
	copy(next.Constraints.Unique, parent.Constraints.Unique)
	next.Constraints.Unique[len(parent.Constraints.Unique)] = constraint
	if idx.Revision() == ids.DefinitionRevision(^uint64(0)) {
		prepared.Err = fmt.Errorf("stuntgres: definition revision exhausted")
		return prepared, nil
	}
	clone := *idx
	clone.ObjectName = name
	clone.ObjectRevision++
	definition := *idx.SQLIndex
	definition.Constraint = UniqueBacking(&constraint)
	clone.SQLIndex = &definition
	prepared.Next, prepared.Replacement = &next, &clone
	return prepared, nil
}
