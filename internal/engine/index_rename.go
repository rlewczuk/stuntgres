package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Called under Engine.mu after namespace relation collisions have been checked.
// Publish parent constraint and backing descriptor together, preserving all key
// and constraint identities and sharing their immutable attribute vectors.
func stageBackingIndexRename(stage indexDescriptorStage, index *catalog.Relation) error {
	parent, ok := stage.StageBase().Relation(index.SQLIndex.Parent)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	collision := func(name string) error {
		if name == index.Name() {
			return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, parent.Name()))
		}
		return nil
	}
	for _, c := range parent.Constraints.NotNull {
		if err := collision(c.Name); err != nil {
			return err
		}
	}
	for _, c := range parent.Constraints.Check {
		if err := collision(c.Name); err != nil {
			return err
		}
	}
	slot := -1
	for i, c := range parent.Constraints.Unique {
		if err := collision(c.Name); err != nil {
			return err
		}
		if c.OID == index.SQLIndex.Constraint.OID && c.Index == index.Address() {
			slot = i
		}
	}
	for i, c := range parent.Constraints.Exclusion {
		if err := collision(c.Name); err != nil {
			return err
		}
		if c.OID == index.SQLIndex.Constraint.OID && c.Index == index.Address() {
			slot = i
		}
	}
	if slot < 0 {
		return ids.ErrCatalogReplaced
	}
	if parent.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *parent
	next.ObjectRevision++
	if index.SQLIndex.IsExclusion() {
		next.Constraints.Exclusion = append([]catalog.ExclusionConstraint(nil), parent.Constraints.Exclusion...)
		next.Constraints.Exclusion[slot].Name = index.Name()
	} else {
		next.Constraints.Unique = append([]catalog.UniqueConstraint(nil), parent.Constraints.Unique...)
		next.Constraints.Unique[slot].Name = index.Name()
	}
	definition := *index.SQLIndex
	constraint := *definition.Constraint
	constraint.Name = index.Name()
	definition.Constraint = &constraint
	index.SQLIndex = &definition
	return stage.Replace(&next)
}
