package engine

import (
	"fmt"

	"stuntgres/internal/engine/ids"
)

// Called under Engine.mu after namespace relation collisions have been checked.
// Publish parent constraint and backing descriptor together, preserving all key
// and constraint identities and sharing their immutable attribute vectors.
func stageBackingIndexRename(stage *CatalogStage, index *relationDescriptor) error {
	parent, ok := stage.Base.Relation(index.SQLIndex.Parent)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	collision := func(name string) error {
		if name == index.Name() {
			return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, parent.Name()))
		}
		return nil
	}
	for _, c := range parent.NotNullConstraints {
		if err := collision(c.Name); err != nil {
			return err
		}
	}
	for _, c := range parent.CheckConstraints {
		if err := collision(c.Name); err != nil {
			return err
		}
	}
	slot := -1
	for i, c := range parent.UniqueConstraints {
		if err := collision(c.Name); err != nil {
			return err
		}
		if c.OID == index.SQLIndex.Constraint.OID && c.Index == index.Address() {
			slot = i
		}
	}
	for i, c := range parent.ExclusionConstraints {
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
		next.ExclusionConstraints = append([]exclusionConstraint(nil), parent.ExclusionConstraints...)
		next.ExclusionConstraints[slot].Name = index.Name()
	} else {
		next.UniqueConstraints = append([]uniqueConstraint(nil), parent.UniqueConstraints...)
		next.UniqueConstraints[slot].Name = index.Name()
	}
	definition := *index.SQLIndex
	constraint := *definition.Constraint
	constraint.Name = index.Name()
	definition.Constraint = &constraint
	index.SQLIndex = &definition
	return stage.Replace(&next)
}
