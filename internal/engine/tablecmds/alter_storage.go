package tablecmds

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func stageColumnStorageIndexes(stage *core.CatalogStage, parent *catalog.Relation, slot int, action parser.AlterTableActionKind) error {
	attribute := parent.Attributes[slot]
	properties := parent.ColumnStorage(slot)
	for _, relation := range stage.StageRead().Relations() {
		definition := relation.SQLIndex
		if definition == nil || definition.Parent != parent.Address() { continue }
		for i := range relation.Columns {
			selected := int32(0)
			if i < len(definition.Keys) { selected = definition.Keys[i].Attribute } else { selected = definition.Include[i-len(definition.Keys)] }
			if selected != attribute { continue }
			value := relation.ColumnStorage(i)
			if action == parser.SetStorage { value.Storage = properties.Storage } else { value.Compression = properties.Compression }
			next := relation.WithColumnStorage(i, value)
			if next != relation {
				if original, exists := stage.Base.Relation(relation.Address()); exists && relation.Revision() == original.Revision() {
					if err := table.BumpAlterRevision(original, next); err != nil { return err }
				}
				if err := stage.Replace(next); err != nil { return err }
			}
			break // PostgreSQL updates only the first simple occurrence.
		}
	}
	return nil
}
