package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Eligibility is captured once from the actual ordered provider input. TYPE
// reminting remains the existing stage owner's separate metadata effect.
func captureUsingIndexDefinition(reader definitionReader, parent *relationDescriptor, raw *parser.UsingIndexConstraint) (usingIndexAttachment, error) {
	address := reader.lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.namespace, Name: raw.Index.Text})
	descriptor, _ := reader.descriptor(address)
	if descriptor == nil {
		return usingIndexAttachment{}, expr.ErrorAt("42704", fmt.Sprintf(`index "%s" does not exist`, raw.Index.Text), raw.Pos)
	}
	index, ok := descriptor.(*relationDescriptor)
	if !ok || index.sqlIndex == nil {
		return usingIndexAttachment{}, sqlError("42809", fmt.Sprintf(`"%s" is not an index`, raw.Index.Text))
	}
	attributes, err := usingIndexEligibility(parent, index, raw)
	if err != nil {
		return usingIndexAttachment{}, err
	}
	return usingIndexAttachment{syntax: raw, index: index, attributes: attributes}, nil
}

func captureUsingIndexDefinitions(reader definitionReader, parent *relationDescriptor, actions []parser.AlterTableAction) ([]usingIndexAttachment, error) {
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
func usingIndexNotNullCandidate(op *Operation, view CatalogRead, reader definitionReader, parent *relationDescriptor, captured usingIndexAttachment) (*relationDescriptor, error) {
	if !captured.syntax.Primary {
		return parent, nil
	}
	previous := op.definition
	op.definition = reader
	defer func() { op.definition = previous }()
	next := parent
	for _, attribute := range captured.attributes {
		slot := next.indexForAttribute(attribute)
		if slot < 0 {
			return nil, ErrCatalogReplaced
		}
		action := parser.AlterTableAction{Action: parser.AddNotNull, Column: parser.Identifier{Text: next.columns[slot].Name}}
		var err error
		next, err = notNullCandidate(op, view, next, action, notNullDirect)
		if err != nil {
			return nil, err
		}
	}
	return next, nil
}

type usingIndexPreparation struct {
	parent, next       *relationDescriptor
	index, replacement *relationDescriptor
	notice             sqlstate.Error
	err                error
}

// Capture the actual physical attachment metadata once; only the stage adapter
// installs its index name/backing link. No tuple/index build runs here.
func (e *Engine) prepareUsingIndexMetadata(op *Operation, view CatalogRead, reader definitionReader, parent *relationDescriptor, captured usingIndexAttachment) (*usingIndexPreparation, error) {
	prepared := &usingIndexPreparation{parent: parent}
	if !captured.notNullPrepared {
		candidate, err := usingIndexNotNullCandidate(op, view, reader, parent, captured)
		if err != nil {
			return nil, err
		}
		parent = candidate
	}
	descriptor, present := reader.descriptor(captured.index.address)
	index, ok := descriptor.(*relationDescriptor)
	if !present || !ok || index.sqlIndex == nil {
		return nil, ErrCatalogReplaced
	}
	prepared.index = index
	name := captured.syntax.Name.Text
	if name == "" {
		name = index.name
	}
	if name != index.name {
		prepared.notice = sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`ALTER TABLE / ADD CONSTRAINT USING INDEX will rename index "%s" to "%s"`, index.name, name)}
	}
	destination := reader.lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.namespace, Name: name})
	if destination != (ids.ObjectAddress{}) && destination != index.address {
		prepared.err = sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
		return prepared, nil
	}
	if captured.syntax.Primary {
		for _, constraint := range parent.uniqueConstraints {
			if constraint.Primary {
				prepared.err = sqlError("42P16", fmt.Sprintf(`multiple primary keys for table "%s" are not allowed`, parent.name))
				return prepared, nil
			}
		}
		if index.sqlIndex.nullsNotDistinct {
			prepared.err = sqlError("42P16", "primary keys cannot use NULLS NOT DISTINCT indexes")
			return prepared, nil
		}
		for _, constraint := range parent.notNullConstraints {
			if constraint.NotValid && slices.Contains(captured.attributes, constraint.Attribute) {
				prepared.err = incompatibleNotValid(constraint.Name, parent.name)
				return prepared, nil
			}
		}
	}
	if index.sqlIndex.constraint != nil || relationConstraintNameExists(parent, name) {
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
	constraint := uniqueConstraint{OID: oid, Name: name, IsLocal: true, Attributes: captured.attributes, Primary: captured.syntax.Primary, NullsNotDistinct: index.sqlIndex.nullsNotDistinct, Index: index.address}
	next := *parent
	next.uniqueConstraints = make([]uniqueConstraint, len(parent.uniqueConstraints)+1)
	copy(next.uniqueConstraints, parent.uniqueConstraints)
	next.uniqueConstraints[len(parent.uniqueConstraints)] = constraint
	if index.revision == ids.DefinitionRevision(^uint64(0)) {
		prepared.err = fmt.Errorf("stuntgres: definition revision exhausted")
		return prepared, nil
	}
	clone := *index
	clone.name = name
	clone.revision++
	definition := *index.sqlIndex
	definition.constraint = uniqueBacking(&constraint)
	clone.sqlIndex = &definition
	prepared.next, prepared.replacement = &next, &clone
	return prepared, nil
}
