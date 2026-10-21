package table

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
)

func PhysicalImportedIndexMatches(source *catalog.Relation, constructor *PhysicalNotNullConstructor, current *catalog.Relation, primary *catalog.UniqueConstraint) bool {
	if source == nil || source.Address() != primary.Index || source.Name() != constructor.IndexName || source.Namespace() != current.Namespace() || source.SQLIndex == nil || source.SQLIndex.Parent != current.Address() || source.SQLIndex.Constraint != nil || len(source.SQLIndex.Keys) != len(primary.Attributes) || len(source.Columns) < len(primary.Attributes) {
		return false
	}
	for i, attribute := range primary.Attributes {
		key, expression, err := index.CatalogKey(constructor.Before, source.SQLIndex.Keys[i])
		slot := current.IndexForAttribute(attribute)
		if err != nil || expression != nil || key != attribute || slot < 0 || source.Columns[i].Usage != current.Columns[slot].Usage {
			return false
		}
	}
	return true
}

func ValidateDeferredPhysicalPrimary(constructor *PhysicalNotNullConstructor, current *catalog.Relation) error {
	reject := func() error { return sqlError("0A000", RestorePhysicalNotNullBoundary) }
	definition := constructor.Deferred
	var primary *catalog.UniqueConstraint
	for i := range current.Constraints.Unique {
		if current.Constraints.Unique[i].Primary == definition.Primary && current.Constraints.Unique[i].Name == definition.Name.Text {
			primary = &current.Constraints.Unique[i]
			break
		}
	}
	if primary == nil || primary.OID == 0 || len(primary.Attributes) != len(definition.Columns) {
		return reject()
	}
	for i, attribute := range primary.Attributes {
		slot, before := current.IndexForAttribute(attribute), constructor.Before.IndexForAttribute(attribute)
		if slot < 0 || before < 0 || current.Columns[slot].Name != definition.Columns[i].Text || current.Columns[slot].Usage != constructor.Before.Columns[before].Usage {
			return reject()
		}
	}
	for attribute, proof := range constructor.Before.Constraints.PhysicalNotNull {
		retained, exists := current.Constraints.PhysicalNotNull[attribute]
		if !exists || retained.Cause != proof.Cause || retained.Usage != proof.Usage {
			return reject()
		}
		if constructor.Competing {
			if retained.Support != proof.Support || retained.Retired != proof.Retired {
				return reject()
			}
		} else if catalog.PhysicalPrimaryForAttribute(current, attribute) != nil {
			return reject()
		}
	}
	return nil
}
