package engine

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/parser"
)

// CaptureUsingIndexes captures every ADD ... USING INDEX of actions before
// any attachment changes an index's name or ownership; the stage already holds
// the effective DROP and TYPE shape. An index a TYPE change affects is
// reminted with a new identity first.
func CaptureUsingIndexes(h index.Host, parent *catalog.Relation, actions []parser.AlterTableAction, transformations []index.ColumnTransformation) ([]index.UsingIndexAttachment, error) {
	var captured []index.UsingIndexAttachment
	for _, action := range actions {
		if action.Action != parser.AddUsingIndex {
			continue
		}
		raw := action.UsingIndex
		attachment, err := index.CaptureUsingIndexDefinition(h.DefinitionReader(), parent, raw)
		if err != nil {
			return nil, err
		}
		idx, attributes := attachment.Index, attachment.Attributes
		// A real TYPE reconstruction precedes attachment and gets a new backing
		// identity. A previous capture of the same index may already have reminted it.
		if h.StageBase().Get(idx.Address()) != nil && index.UniqueIndexTypeAffected(idx.SQLIndex, transformations) {
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
		captured = append(captured, index.UsingIndexAttachment{Syntax: raw, Index: idx, Attributes: attributes})
	}
	return captured, nil
}
