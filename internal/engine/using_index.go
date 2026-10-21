package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Capture all eligibility before any runtime attachment changes its name or
// ownership. The stage already contains the effective DROP/TYPE shape.
func (e *Engine) captureUsingIndexes(stage *CatalogStage, parent *catalog.Relation, actions []parser.AlterTableAction, transformations []index.ColumnTransformation) ([]index.UsingIndexAttachment, error) {
	var captured []index.UsingIndexAttachment
	for _, action := range actions {
		if action.Action != parser.AddUsingIndex {
			continue
		}
		raw := action.UsingIndex
		attachment, err := index.CaptureUsingIndexDefinition(stageDefinitionReader{stage}, parent, raw)
		if err != nil {
			return nil, err
		}
		idx, attributes := attachment.Index, attachment.Attributes
		// A real TYPE reconstruction precedes attachment and gets a new backing
		// identity. A previous capture of the same index may already have reminted it.
		if stage.Base.Get(idx.Address()) != nil && index.UniqueIndexTypeAffected(idx.SQLIndex, transformations) {
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
			e.mu.Lock()
			err = stage.removeObjectLocked(e.catalog.Domain.Ref(stage.Base.Epoch(), idx.Address()), false)
			e.mu.Unlock()
			if err == nil {
				err = stage.Create(&clone)
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

// Runtime effects follow action order, but remain immutable statement candidates.
// Row nullability is validated by the existing final effective-row ALTER scan.
func (e *Engine) stageUsingIndex(op *session.Operation, stage *CatalogStage, parent *catalog.Relation, captured index.UsingIndexAttachment) (*catalog.Relation, error) {
	prepared, err := e.prepareUsingIndexMetadata(op, e.stageRead(stage), stageDefinitionReader{stage}, parent, captured)
	if err != nil {
		return nil, err
	}
	return index.StagePreparedUsingIndex(op, stage, parent, prepared)
}
