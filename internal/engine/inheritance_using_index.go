package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Eligibility is captured once from the actual ordered provider input. TYPE
// reminting remains the existing stage owner's separate metadata effect.
func captureUsingIndexDefinition(reader definitionReader, parent *catalog.Relation, raw *parser.UsingIndexConstraint) (usingIndexAttachment, error) {
	address := reader.lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.Namespace(), Name: raw.Index.Text})
	descriptor, _ := reader.descriptor(address)
	if descriptor == nil {
		return usingIndexAttachment{}, expr.ErrorAt("42704", fmt.Sprintf(`index "%s" does not exist`, raw.Index.Text), raw.Pos)
	}
	index, ok := descriptor.(*catalog.Relation)
	if !ok || index.SQLIndex == nil {
		return usingIndexAttachment{}, sqlError("42809", fmt.Sprintf(`"%s" is not an index`, raw.Index.Text))
	}
	attributes, err := usingIndexEligibility(parent, index, raw)
	if err != nil {
		return usingIndexAttachment{}, err
	}
	return usingIndexAttachment{syntax: raw, index: index, attributes: attributes}, nil
}

func captureUsingIndexDefinitions(reader definitionReader, parent *catalog.Relation, actions []parser.AlterTableAction) ([]usingIndexAttachment, error) {
	var captured []usingIndexAttachment
	for _, action := range actions {
		if action.Action == parser.AddUsingIndex {
			attachment, err := captureUsingIndexDefinition(reader, parent, action.UsingIndex)
			if err != nil {
				return nil, err
			}
			captured = append(captured, attachment)
		}
	}
	return captured, nil
}

// This is the incumbent USING INDEX NN constructor, shared by admission and
// ordinary staging. Only actual eligible KEY attributes participate; INCLUDE
// attributes and the physical index/constraint are never propagated.
func usingIndexNotNullCandidate(op *Operation, view catalog.Read, reader definitionReader, parent *catalog.Relation, captured usingIndexAttachment) (*catalog.Relation, error) {
	if !captured.syntax.Primary {
		return parent, nil
	}
	previous := op.definition
	op.definition = reader
	defer func() { op.definition = previous }()
	next := parent
	for _, attribute := range captured.attributes {
		slot := next.IndexForAttribute(attribute)
		if slot < 0 {
			return nil, ids.ErrCatalogReplaced
		}
		action := parser.AlterTableAction{Action: parser.AddNotNull, Column: parser.Identifier{Text: next.Columns[slot].Name}}
		var err error
		next, err = notNullCandidate(op, view, next, action, notNullDirect)
		if err != nil {
			return nil, err
		}
	}
	return next, nil
}

type usingIndexPreparation struct {
	parent, next       *catalog.Relation
	index, replacement *catalog.Relation
	notice             sqlstate.Error
	err                error
}

// Capture the actual physical attachment metadata once; only the stage adapter
// installs its index name/backing link. No tuple/index build runs here.
func (e *Engine) prepareUsingIndexMetadata(op *Operation, view catalog.Read, reader definitionReader, parent *catalog.Relation, captured usingIndexAttachment) (*usingIndexPreparation, error) {
	prepared := &usingIndexPreparation{parent: parent}
	if !captured.notNullPrepared {
		candidate, err := usingIndexNotNullCandidate(op, view, reader, parent, captured)
		if err != nil {
			return nil, err
		}
		parent = candidate
	}
	descriptor, present := reader.descriptor(captured.index.Address())
	index, ok := descriptor.(*catalog.Relation)
	if !present || !ok || index.SQLIndex == nil {
		return nil, ids.ErrCatalogReplaced
	}
	prepared.index = index
	name := captured.syntax.Name.Text
	if name == "" {
		name = index.Name()
	}
	if name != index.Name() {
		prepared.notice = sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`ALTER TABLE / ADD CONSTRAINT USING INDEX will rename index "%s" to "%s"`, index.Name(), name)}
	}
	destination := reader.lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.Namespace(), Name: name})
	if destination != (ids.ObjectAddress{}) && destination != index.Address() {
		prepared.err = sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
		return prepared, nil
	}
	if captured.syntax.Primary {
		for _, constraint := range parent.UniqueConstraints {
			if constraint.Primary {
				prepared.err = sqlError("42P16", fmt.Sprintf(`multiple primary keys for table "%s" are not allowed`, parent.Name()))
				return prepared, nil
			}
		}
		if index.SQLIndex.NullsNotDistinct {
			prepared.err = sqlError("42P16", "primary keys cannot use NULLS NOT DISTINCT indexes")
			return prepared, nil
		}
		for _, constraint := range parent.NotNullConstraints {
			if constraint.NotValid && slices.Contains(captured.attributes, constraint.Attribute) {
				prepared.err = incompatibleNotValid(constraint.Name, parent.Name())
				return prepared, nil
			}
		}
	}
	if index.SQLIndex.Constraint != nil || relationConstraintNameExists(parent, name) {
		prepared.err = usingIndexCatalogDuplicate(parent, name)
		return prepared, nil
	}
	e.mu.Lock()
	oid, err := e.catalog.allocateOIDLocked()
	e.mu.Unlock()
	if err != nil {
		prepared.err = err
		return prepared, nil
	}
	constraint := catalog.UniqueConstraint{OID: oid, Name: name, IsLocal: true, Attributes: captured.attributes, Primary: captured.syntax.Primary, NullsNotDistinct: index.SQLIndex.NullsNotDistinct, Index: index.Address()}
	next := *parent
	next.UniqueConstraints = make([]catalog.UniqueConstraint, len(parent.UniqueConstraints)+1)
	copy(next.UniqueConstraints, parent.UniqueConstraints)
	next.UniqueConstraints[len(parent.UniqueConstraints)] = constraint
	if index.Revision() == ids.DefinitionRevision(^uint64(0)) {
		prepared.err = fmt.Errorf("stuntgres: definition revision exhausted")
		return prepared, nil
	}
	clone := *index
	clone.ObjectName = name
	clone.ObjectRevision++
	definition := *index.SQLIndex
	definition.Constraint = uniqueBacking(&constraint)
	clone.SQLIndex = &definition
	prepared.next, prepared.replacement = &next, &clone
	return prepared, nil
}
