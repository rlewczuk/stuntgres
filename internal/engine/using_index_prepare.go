package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// Capture the actual physical attachment metadata once; only the stage adapter
// installs its index name/backing link. No tuple/index build runs here.
func (e *Engine) prepareUsingIndexMetadata(op *session.Operation, view catalog.Read, reader catalog.DefinitionReader, parent *catalog.Relation, captured usingIndexAttachment) (*usingIndexPreparation, error) {
	prepared := &usingIndexPreparation{Parent: parent}
	if !captured.NotNullPrepared {
		candidate, err := usingIndexNotNullCandidate(op, view, reader, parent, captured)
		if err != nil {
			return nil, err
		}
		parent = candidate
	}
	descriptor, present := reader.Descriptor(captured.Index.Address())
	index, ok := descriptor.(*catalog.Relation)
	if !present || !ok || index.SQLIndex == nil {
		return nil, ids.ErrCatalogReplaced
	}
	prepared.Index = index
	name := captured.Syntax.Name.Text
	if name == "" {
		name = index.Name()
	}
	if name != index.Name() {
		prepared.Notice = sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`ALTER TABLE / ADD CONSTRAINT USING INDEX will rename index "%s" to "%s"`, index.Name(), name)}
	}
	destination := reader.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.Namespace(), Name: name})
	if destination != (ids.ObjectAddress{}) && destination != index.Address() {
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
		if index.SQLIndex.NullsNotDistinct {
			prepared.Err = sqlError("42P16", "primary keys cannot use NULLS NOT DISTINCT indexes")
			return prepared, nil
		}
		for _, constraint := range parent.Constraints.NotNull {
			if constraint.NotValid && slices.Contains(captured.Attributes, constraint.Attribute) {
				prepared.Err = incompatibleNotValid(constraint.Name, parent.Name())
				return prepared, nil
			}
		}
	}
	if index.SQLIndex.Constraint != nil || catalog.RelationConstraintNameExists(parent, name) {
		prepared.Err = UsingIndexCatalogDuplicate(parent, name)
		return prepared, nil
	}
	e.mu.Lock()
	oid, err := e.catalog.allocateOIDLocked()
	e.mu.Unlock()
	if err != nil {
		prepared.Err = err
		return prepared, nil
	}
	constraint := catalog.UniqueConstraint{OID: oid, Name: name, IsLocal: true, Attributes: captured.Attributes, Primary: captured.Syntax.Primary, NullsNotDistinct: index.SQLIndex.NullsNotDistinct, Index: index.Address()}
	next := *parent
	next.Constraints.Unique = make([]catalog.UniqueConstraint, len(parent.Constraints.Unique)+1)
	copy(next.Constraints.Unique, parent.Constraints.Unique)
	next.Constraints.Unique[len(parent.Constraints.Unique)] = constraint
	if index.Revision() == ids.DefinitionRevision(^uint64(0)) {
		prepared.Err = fmt.Errorf("stuntgres: definition revision exhausted")
		return prepared, nil
	}
	clone := *index
	clone.ObjectName = name
	clone.ObjectRevision++
	definition := *index.SQLIndex
	definition.Constraint = UniqueBacking(&constraint)
	clone.SQLIndex = &definition
	prepared.Next, prepared.Replacement = &next, &clone
	return prepared, nil
}
