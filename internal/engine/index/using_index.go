package index

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type UsingIndexAttachment struct {
	Syntax          *parser.UsingIndexConstraint
	Index           *catalog.Relation
	Attributes      []int32
	NotNullPrepared bool
}

func UsingIndexEligibility(view catalog.Read, parent, index *catalog.Relation, raw *parser.UsingIndexConstraint) ([]int32, error) {
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
		attribute, expression, err := CatalogKey(parent, key)
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
	if definition.Method != catalog.BtreeMethod {
		return fail("42809", fmt.Sprintf(`index "%s" is not a btree`, name), false)
	}
	for i, key := range definition.Keys {
		usage := parent.Columns[slices.Index(parent.Attributes, attributes[i])].Usage
		class, ok, err := view.DefaultIndexClass(definition.MethodIdentity(), usage.Type)
		if err != nil {
			return nil, err
		}
		keyUsage, err := KeyUsage(parent, key)
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

func UsingIndexCatalogDuplicate(parent *catalog.Relation, name string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "pg_constraint_conrelid_contypid_conname_index"`, Detail: fmt.Sprintf("Key (conrelid, contypid, conname)=(%d, 0, %s) already exists.", parent.Address().ObjectID, name), Schema: "pg_catalog", Table: "pg_constraint", Constraint: "pg_constraint_conrelid_contypid_conname_index"}
}

// UsingIndexStage is what attaching a prepared USING INDEX needs: the stage
// and the statement's notices. Neither needs Engine.mu, and attaching takes
// none.
type UsingIndexStage interface {
	DescriptorStage() DescriptorStage
	EmitNotice(session.NoticeLevel, sqlstate.Error)
}

// StageUsingIndex attaches a prepared USING INDEX to parent: it emits the
// rename NOTICE, reports the preparation's deferred error, then renames the
// index and links it to its constraint.
func StageUsingIndex(h UsingIndexStage, parent *catalog.Relation, prepared *UsingIndexPreparation) (*catalog.Relation, error) {
	if prepared == nil || prepared.Parent != parent {
		return nil, ids.ErrCatalogReplaced
	}
	stage := h.DescriptorStage()
	index, clone := prepared.Index, prepared.Replacement
	current := stage.Get(index.Address())
	if current == nil {
		current = stage.StageBase().Get(index.Address())
	}
	if current != index {
		return nil, ids.ErrCatalogReplaced
	}
	if prepared.Notice.Code != "" {
		h.EmitNotice(session.LevelNotice, prepared.Notice)
	}
	if prepared.Err != nil {
		return nil, prepared.Err
	}
	if stage.StageBase().Get(index.Address()) == nil {
		// Preserve the existing TYPE remint's original removal/name observation.
		key := catalog.DescriptorNameKey(clone)
		stage.RenameStagedDescriptor(catalog.DescriptorNameKey(index), clone, key)
	} else if err := stage.Replace(clone); err != nil {
		return nil, err
	}
	return prepared.Next, nil
}

// CREATE's inline UNIQUE/PK surface cannot represent included attributes.
// Emit the actual backing index and attach it through the selected ALTER path.
func UsingIndexDump(index *catalog.Relation) bool {
	return index.SQLIndex != nil && index.SQLIndex.Constraint != nil && index.SQLIndex.Constraint.Kind == catalog.UniqueBackingKind && len(index.SQLIndex.Include) != 0
}

// CaptureUsingIndexes captures every ADD ... USING INDEX of actions before
// any attachment changes an index's name or ownership; the stage already holds
// the effective DROP and TYPE shape. An index a TYPE change affects is
// reminted with a new identity first.
func CaptureUsingIndexes(h Host, parent *catalog.Relation, actions []parser.AlterTableAction, transformations []ColumnTransformation) ([]UsingIndexAttachment, error) {
	var captured []UsingIndexAttachment
	for _, action := range actions {
		if action.Action != parser.AddUsingIndex {
			continue
		}
		raw := action.UsingIndex
		attachment, err := CaptureUsingIndexDefinition(h.StageBase(), h.DefinitionReader(), parent, raw)
		if err != nil {
			return nil, err
		}
		idx, attributes := attachment.Index, attachment.Attributes
		// A real TYPE reconstruction precedes attachment and gets a new backing
		// identity. A previous capture of the same index may already have reminted it.
		if h.StageBase().Get(idx.Address()) != nil && UniqueIndexTypeAffected(idx.SQLIndex, transformations) {
			id, allocateErr := h.AllocateOID()
			var revision ids.DefinitionRevision
			if allocateErr == nil {
				revision, allocateErr = h.Revision()
			}
			if allocateErr != nil {
				return nil, allocateErr
			}
			clone := *idx
			clone.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}
			clone.ObjectRevision = revision
			clone.Columns = slices.Clone(idx.Columns)
			for i, attribute := range attributes {
				clone.Columns[i].Name = parent.Columns[slices.Index(parent.Attributes, attribute)].Name
				clone.Columns[i].OriginTable = uint32(id)
			}
			for i, attribute := range idx.SQLIndex.Include {
				clone.Columns[len(attributes)+i].Name = parent.Columns[slices.Index(parent.Attributes, attribute)].Name
				clone.Columns[len(attributes)+i].OriginTable = uint32(id)
			}
			for j, column := range clone.Columns {
				if column.Name != idx.Columns[j].Name {
					clone.Index = make(map[string]int, len(clone.Columns))
					for slot, renamed := range clone.Columns {
						clone.Index[renamed.Name] = slot
					}
					break
				}
			}
			err = h.RemoveObject(stageRef(h, idx.Address()), false)
			if err == nil {
				err = h.Stage().Create(&clone)
			}
			if err != nil {
				return nil, err
			}
			idx = &clone
		}
		captured = append(captured, UsingIndexAttachment{Syntax: raw, Index: idx, Attributes: attributes})
	}
	return captured, nil
}
