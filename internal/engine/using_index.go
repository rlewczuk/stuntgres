package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type usingIndexAttachment struct {
	syntax     *parser.UsingIndexConstraint
	index      *relationDescriptor
	attributes []int32
}

// Capture all eligibility before any runtime attachment changes its name or
// ownership. The stage already contains the effective DROP/TYPE shape.
func (e *Engine) captureUsingIndexes(stage *CatalogStage, parent *relationDescriptor, actions []parser.AlterTableAction, transformations []columnTransformation) ([]usingIndexAttachment, error) {
	var captured []usingIndexAttachment
	for _, action := range actions {
		if action.Action != parser.AddUsingIndex {
			continue
		}
		raw := action.UsingIndex
		address := stage.lookupName(NameKey{ClassID: RelationClassID, Namespace: parent.namespace, Name: raw.Index.Text})
		descriptor := stage.descriptors[address]
		if descriptor == nil {
			descriptor = stage.base.state.descriptors[address]
		}
		if descriptor == nil {
			return nil, expressionError("42704", fmt.Sprintf(`index "%s" does not exist`, raw.Index.Text), raw.Pos)
		}
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			return nil, sqlError("42809", fmt.Sprintf(`"%s" is not an index`, raw.Index.Text))
		}
		attributes, err := usingIndexEligibility(parent, index, raw)
		if err != nil {
			return nil, err
		}
		// A real TYPE reconstruction precedes attachment and gets a new backing
		// identity. A previous capture of the same index may already have reminted it.
		if stage.base.state.descriptors[index.address] != nil && uniqueIndexTypeAffected(index.sqlIndex, transformations) {
			e.mu.Lock()
			id, allocateErr := e.allocateOIDLocked()
			var revision DefinitionRevision
			if allocateErr == nil {
				revision, allocateErr = e.revisionLocked()
			}
			e.mu.Unlock()
			if allocateErr != nil {
				return nil, allocateErr
			}
			clone := *index
			clone.address = ObjectAddress{ClassID: RelationClassID, ObjectID: id}
			clone.revision = revision
			clone.columns = slices.Clone(index.columns)
			for i, attribute := range attributes {
				clone.columns[i].Name = parent.columns[slices.Index(parent.attributes, attribute)].Name
				clone.columns[i].OriginTable = uint32(id)
			}
			for i, attribute := range index.sqlIndex.include {
				clone.columns[len(attributes)+i].Name = parent.columns[slices.Index(parent.attributes, attribute)].Name
				clone.columns[len(attributes)+i].OriginTable = uint32(id)
			}
			for j, column := range clone.columns {
				if column.Name != index.columns[j].Name {
					clone.index = make(map[string]int, len(clone.columns))
					for slot, renamed := range clone.columns {
						clone.index[renamed.Name] = slot
					}
					break
				}
			}
			e.mu.Lock()
			err = stage.removeObjectLocked(ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: index.address}, false)
			e.mu.Unlock()
			if err == nil {
				err = stage.Create(&clone)
			}
			if err != nil {
				return nil, err
			}
			index = &clone
		}
		captured = append(captured, usingIndexAttachment{syntax: raw, index: index, attributes: attributes})
	}
	return captured, nil
}

func usingIndexEligibility(parent, index *relationDescriptor, raw *parser.UsingIndexConstraint) ([]int32, error) {
	definition := index.sqlIndex
	fail := func(code, message string, detail bool) ([]int32, error) {
		diagnostic := expressionError(code, message, raw.Pos).(*sqlstate.Error)
		if detail {
			diagnostic.Detail = "Cannot create a primary key or unique constraint using such an index."
		}
		return nil, diagnostic
	}
	name := index.name
	if definition.constraint != nil {
		return fail("55000", fmt.Sprintf(`index "%s" is already associated with a constraint`, name), false)
	}
	if definition.parent != parent.address {
		return fail("55000", fmt.Sprintf(`index "%s" does not belong to table "%s"`, name, parent.name), false)
	}
	if !definition.valid {
		return fail("55000", fmt.Sprintf(`index "%s" is not valid`, name), false)
	}
	if !definition.unique {
		return fail("42809", fmt.Sprintf(`"%s" is not a unique index`, name), true)
	}
	if len(definition.keys) == 0 || len(definition.keys) > 32 {
		return nil, fmt.Errorf("stuntgres: invalid attachment key count")
	}
	var attributes [32]int32
	for i, key := range definition.keys {
		attribute, expression, err := indexCatalogKey(parent, key)
		if err != nil {
			return nil, err
		}
		if expression != nil {
			return fail("42809", fmt.Sprintf(`index "%s" contains expressions`, name), true)
		}
		attributes[i] = attribute
	}
	if definition.predicate != nil {
		return fail("42809", fmt.Sprintf(`"%s" is a partial index`, name), true)
	}
	// Deferred native UNIQUE is not represented. Associated EXCLUDE indexes have
	// already failed above; standalone represented indexes are inherently immediate.
	if definition.method != btreeMethod {
		return fail("42809", fmt.Sprintf(`index "%s" is not a btree`, name), false)
	}
	for i, key := range definition.keys {
		usage := parent.columns[slices.Index(parent.attributes, attributes[i])].Usage
		class, ok := sqltype.DefaultIndexClass(usage.Type)
		keyUsage, err := indexKeyUsage(parent, key)
		if err != nil {
			return nil, err
		}
		if !ok || key.class.OID() != class.OID() || !key.class.SameInferenceIdentity(class) || keyUsage.Collation.ID != usage.Collation.ID || key.desc || key.nullsFirst {
			return fail("42809", fmt.Sprintf(`index "%s" column number %d does not have default sorting behavior`, name, i+1), true)
		}
	}
	for i, attribute := range attributes[:len(definition.keys)] {
		if slices.Contains(attributes[:i], attribute) {
			kind := "unique"
			if raw.Primary {
				kind = "primary key"
			}
			return fail("42701", fmt.Sprintf(`column "%s" appears twice in %s constraint`, parent.columns[slices.Index(parent.attributes, attribute)].Name, kind), false)
		}
	}
	return slices.Clone(attributes[:len(definition.keys)]), nil
}

func usingIndexCatalogDuplicate(parent *relationDescriptor, name string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "pg_constraint_conrelid_contypid_conname_index"`, Detail: fmt.Sprintf("Key (conrelid, contypid, conname)=(%d, 0, %s) already exists.", parent.address.ObjectID, name), Schema: "pg_catalog", Table: "pg_constraint", Constraint: "pg_constraint_conrelid_contypid_conname_index"}
}

// Runtime effects follow action order, but remain immutable statement candidates.
// Row nullability is validated by the existing final effective-row ALTER scan.
func (e *Engine) stageUsingIndex(op *Operation, stage *CatalogStage, parent *relationDescriptor, captured usingIndexAttachment) (*relationDescriptor, error) {
	index := captured.index
	if current, ok := stage.descriptors[index.address].(*relationDescriptor); ok {
		index = current
	}
	name := captured.syntax.Name.Text
	if name == "" {
		name = index.name
	}
	if name != index.name {
		op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`ALTER TABLE / ADD CONSTRAINT USING INDEX will rename index "%s" to "%s"`, index.name, name)})
	}
	destination := stage.lookupName(NameKey{ClassID: RelationClassID, Namespace: parent.namespace, Name: name})
	if destination != (ObjectAddress{}) && destination != index.address {
		return nil, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
	}
	if captured.syntax.Primary {
		for _, c := range parent.uniqueConstraints {
			if c.Primary {
				return nil, sqlError("42P16", fmt.Sprintf(`multiple primary keys for table "%s" are not allowed`, parent.name))
			}
		}
		if index.sqlIndex.nullsNotDistinct {
			return nil, sqlError("42P16", "primary keys cannot use NULLS NOT DISTINCT indexes")
		}
	}
	// Renaming an index attached earlier in this same action list also renames
	// that constraint; inserting another identity then collides under the new name.
	if index.sqlIndex.constraint != nil {
		return nil, usingIndexCatalogDuplicate(parent, name)
	}
	if relationConstraintNameExists(parent, name) {
		return nil, usingIndexCatalogDuplicate(parent, name)
	}
	e.mu.Lock()
	oid, err := e.allocateOIDLocked()
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	constraint := uniqueConstraint{OID: oid, Name: name, Attributes: captured.attributes, Primary: captured.syntax.Primary, NullsNotDistinct: index.sqlIndex.nullsNotDistinct, Index: index.address}
	next := *parent
	next.uniqueConstraints = make([]uniqueConstraint, len(parent.uniqueConstraints)+1)
	copy(next.uniqueConstraints, parent.uniqueConstraints)
	next.uniqueConstraints[len(parent.uniqueConstraints)] = constraint
	if constraint.Primary {
		missing := 0
		for _, attribute := range constraint.Attributes {
			found := false
			for _, n := range parent.notNullConstraints {
				found = found || n.Attribute == attribute
			}
			if !found {
				missing++
			}
		}
		if missing != 0 {
			next.columns = slices.Clone(parent.columns)
			next.notNullConstraints = make([]notNullConstraint, len(parent.notNullConstraints), len(parent.notNullConstraints)+missing)
			copy(next.notNullConstraints, parent.notNullConstraints)
			used := constraintNames(e.stageRead(stage), parent.namespace, parent)
			used[name] = true
			for slot, attribute := range parent.attributes {
				if !slices.Contains(constraint.Attributes, attribute) {
					continue
				}
				found := false
				for _, n := range parent.notNullConstraints {
					found = found || n.Attribute == attribute
				}
				if found {
					continue
				}
				label := generatedConstraintName(parent.name, parent.columns[slot].Name, "not_null", used)
				used[label] = true
				next.columns[slot].Nullable = false
				next.columns[slot].NullabilityKnown = true
				next.notNullConstraints = append(next.notNullConstraints, notNullConstraint{Name: label, Attribute: attribute})
			}
		}
	}
	if index.revision == DefinitionRevision(^uint64(0)) {
		return nil, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	clone := *index
	clone.name = name
	clone.revision++
	definition := *index.sqlIndex
	definition.constraint = uniqueBacking(&constraint)
	clone.sqlIndex = &definition
	if stage.base.state.descriptors[index.address] == nil {
		// TYPE already created this reminted identity in the private candidate.
		// Replace fences existing base identities; keep this creation and its
		// original removal observation while updating its private name slot.
		delete(stage.names, descriptorNameKey(index))
		stage.descriptors[index.address] = &clone
		key := descriptorNameKey(&clone)
		stage.names[key] = clone.address
		stage.touched[key] = stage.base.state.names[key]
	} else if err := stage.Replace(&clone); err != nil {
		return nil, err
	}
	return &next, nil
}

// CREATE's inline UNIQUE/PK surface cannot represent included attributes.
// Emit the actual backing index and attach it through the selected ALTER path.
func usingIndexDump(index *relationDescriptor) bool {
	return index.sqlIndex != nil && index.sqlIndex.constraint != nil && index.sqlIndex.constraint.kind == uniqueBackingKind && len(index.sqlIndex.include) != 0
}
