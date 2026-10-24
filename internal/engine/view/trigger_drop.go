package view

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
)

// TriggerPartitionDropError describes the exact owning parent trigger. Partition
// dependencies require an owner in the retirement closure even under CASCADE.
func TriggerPartitionDropError(read catalog.Read, relation *catalog.Relation, trigger stored.Trigger, path []ids.ObjectAddress) error {
	parent, definition := stored.TriggerParent(read, trigger.Parts().Parent)
	if parent == nil {
		return ids.ErrCatalogReplaced
	}
	object := "trigger " + catalog.DependencyIdentifier(trigger.Name()) + " on table " + stored.DependencyRelationName(read, relation, path)
	owner := "trigger " + catalog.DependencyIdentifier(definition.Name()) + " on table " + stored.DependencyRelationName(read, parent, path)
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop " + object + " because " + owner + " requires it", Hint: "You can drop " + owner + " instead."}
}
