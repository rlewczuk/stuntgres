package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type usingIndexAttachment struct {
	syntax          *parser.UsingIndexConstraint
	index           *relationDescriptor
	attributes      []int32
	notNullPrepared bool
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
		attachment, err := captureUsingIndexDefinition(stageDefinitionReader{stage}, parent, raw)
		if err != nil {
			return nil, err
		}
		index, attributes := attachment.index, attachment.attributes
		// A real TYPE reconstruction precedes attachment and gets a new backing
		// identity. A previous capture of the same index may already have reminted it.
		if stage.base.Get(index.Address()) != nil && uniqueIndexTypeAffected(index.SQLIndex, transformations) {
			e.mu.Lock()
			id, allocateErr := e.catalog.allocateOIDLocked()
			var revision ids.DefinitionRevision
			if allocateErr == nil {
				revision, allocateErr = e.catalog.revisionLocked()
			}
			e.mu.Unlock()
			if allocateErr != nil {
				return nil, allocateErr
			}
			clone := *index
			clone.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}
			clone.ObjectRevision = revision
			clone.Columns = slices.Clone(index.Columns)
			for i, attribute := range attributes {
				clone.Columns[i].Name = parent.Columns[slices.Index(parent.Attributes, attribute)].Name
				clone.Columns[i].OriginTable = uint32(id)
			}
			for i, attribute := range index.SQLIndex.Include {
				clone.Columns[len(attributes)+i].Name = parent.Columns[slices.Index(parent.Attributes, attribute)].Name
				clone.Columns[len(attributes)+i].OriginTable = uint32(id)
			}
			for j, column := range clone.Columns {
				if column.Name != index.Columns[j].Name {
					clone.Index = make(map[string]int, len(clone.Columns))
					for slot, renamed := range clone.Columns {
						clone.Index[renamed.Name] = slot
					}
					break
				}
			}
			e.mu.Lock()
			err = stage.removeObjectLocked(e.catalog.Domain.Ref(stage.base.Epoch(), index.Address()), false)
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
	definition := index.SQLIndex
	fail := func(code, message string, detail bool) ([]int32, error) {
		diagnostic := expr.ErrorAt(code, message, raw.Pos).(*sqlstate.Error)
		if detail {
			diagnostic.Detail = "Cannot create a primary key or unique constraint using such an index."
		}
		return nil, diagnostic
	}
	name := index.Name()
	if definition.Constraint != nil {
		return fail("55000", fmt.Sprintf(`index "%s" is already associated with a constraint`, name), false)
	}
	if definition.Parent != parent.Address() {
		return fail("55000", fmt.Sprintf(`index "%s" does not belong to table "%s"`, name, parent.Name()), false)
	}
	if !definition.Valid {
		return fail("55000", fmt.Sprintf(`index "%s" is not valid`, name), false)
	}
	if !definition.Unique {
		return fail("42809", fmt.Sprintf(`"%s" is not a unique index`, name), true)
	}
	if len(definition.Keys) == 0 || len(definition.Keys) > 32 {
		return nil, fmt.Errorf("stuntgres: invalid attachment key count")
	}
	var attributes [32]int32
	for i, key := range definition.Keys {
		attribute, expression, err := indexCatalogKey(parent, key)
		if err != nil {
			return nil, err
		}
		if expression != nil {
			return fail("42809", fmt.Sprintf(`index "%s" contains expressions`, name), true)
		}
		attributes[i] = attribute
	}
	if definition.Predicate != nil {
		return fail("42809", fmt.Sprintf(`"%s" is a partial index`, name), true)
	}
	// Deferred native UNIQUE is not represented. Associated EXCLUDE indexes have
	// already failed above; standalone represented indexes are inherently immediate.
	if definition.Method != btreeMethod {
		return fail("42809", fmt.Sprintf(`index "%s" is not a btree`, name), false)
	}
	for i, key := range definition.Keys {
		usage := parent.Columns[slices.Index(parent.Attributes, attributes[i])].Usage
		class, ok := sqltype.DefaultIndexClass(usage.Type)
		keyUsage, err := indexKeyUsage(parent, key)
		if err != nil {
			return nil, err
		}
		if !ok || key.Class.OID() != class.OID() || !key.Class.SameInferenceIdentity(class) || keyUsage.Collation.ID != usage.Collation.ID || key.Desc || key.NullsFirst {
			return fail("42809", fmt.Sprintf(`index "%s" column number %d does not have default sorting behavior`, name, i+1), true)
		}
	}
	for i, attribute := range attributes[:len(definition.Keys)] {
		if slices.Contains(attributes[:i], attribute) {
			kind := "unique"
			if raw.Primary {
				kind = "primary key"
			}
			return fail("42701", fmt.Sprintf(`column "%s" appears twice in %s constraint`, parent.Columns[slices.Index(parent.Attributes, attribute)].Name, kind), false)
		}
	}
	return slices.Clone(attributes[:len(definition.Keys)]), nil
}

func usingIndexCatalogDuplicate(parent *relationDescriptor, name string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "pg_constraint_conrelid_contypid_conname_index"`, Detail: fmt.Sprintf("Key (conrelid, contypid, conname)=(%d, 0, %s) already exists.", parent.Address().ObjectID, name), Schema: "pg_catalog", Table: "pg_constraint", Constraint: "pg_constraint_conrelid_contypid_conname_index"}
}

// Runtime effects follow action order, but remain immutable statement candidates.
// Row nullability is validated by the existing final effective-row ALTER scan.
func (e *Engine) stageUsingIndex(op *Operation, stage *CatalogStage, parent *relationDescriptor, captured usingIndexAttachment) (*relationDescriptor, error) {
	prepared, err := e.prepareUsingIndexMetadata(op, e.stageRead(stage), stageDefinitionReader{stage}, parent, captured)
	if err != nil {
		return nil, err
	}
	return stagePreparedUsingIndex(op, stage, parent, prepared)
}

func stagePreparedUsingIndex(op *Operation, stage *CatalogStage, parent *relationDescriptor, prepared *usingIndexPreparation) (*relationDescriptor, error) {
	if prepared == nil || prepared.parent != parent {
		return nil, ids.ErrCatalogReplaced
	}
	index, clone := prepared.index, prepared.replacement
	current := stage.descriptors[index.Address()]
	if current == nil {
		current = stage.base.Get(index.Address())
	}
	if current != index {
		return nil, ids.ErrCatalogReplaced
	}
	if prepared.notice.Code != "" {
		op.emitNotice(levelNotice, prepared.notice)
	}
	if prepared.err != nil {
		return nil, prepared.err
	}
	if stage.base.Get(index.Address()) == nil {
		// Preserve the existing TYPE remint's original removal/name observation.
		delete(stage.names, descriptorNameKey(index))
		stage.descriptors[index.Address()] = clone
		key := descriptorNameKey(clone)
		stage.names[key] = clone.Address()
		stage.touched[key] = stage.base.NameAddress(key)
	} else if err := stage.Replace(clone); err != nil {
		return nil, err
	}
	return prepared.next, nil
}

// CREATE's inline UNIQUE/PK surface cannot represent included attributes.
// Emit the actual backing index and attach it through the selected ALTER path.
func usingIndexDump(index *relationDescriptor) bool {
	return index.SQLIndex != nil && index.SQLIndex.Constraint != nil && index.SQLIndex.Constraint.Kind == uniqueBackingKind && len(index.SQLIndex.Include) != 0
}
