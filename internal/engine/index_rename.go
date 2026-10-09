package engine

import "fmt"

// Called under Engine.mu after namespace relation collisions have been checked.
// Publish parent constraint and backing descriptor together, preserving all key
// and constraint identities and sharing their immutable attribute vectors.
func stageBackingIndexRename(stage *CatalogStage, index *relationDescriptor) error {
	parent, ok := stage.base.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
	if !ok {
		return ErrCatalogReplaced
	}
	collision := func(name string) error {
		if name == index.name {
			return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, parent.name))
		}
		return nil
	}
	for _, c := range parent.notNullConstraints {
		if err := collision(c.Name); err != nil {
			return err
		}
	}
	for _, c := range parent.checkConstraints {
		if err := collision(c.Name); err != nil {
			return err
		}
	}
	slot := -1
	for i, c := range parent.uniqueConstraints {
		if err := collision(c.Name); err != nil {
			return err
		}
		if c.OID == index.sqlIndex.unique.OID && c.Index == index.address {
			slot = i
		}
	}
	if slot < 0 {
		return ErrCatalogReplaced
	}
	if parent.revision == DefinitionRevision(^uint64(0)) {
		return fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *parent
	next.revision++
	next.uniqueConstraints = append([]uniqueConstraint(nil), parent.uniqueConstraints...)
	next.uniqueConstraints[slot].Name = index.name
	definition := *index.sqlIndex
	unique := *definition.unique
	unique.Name = index.name
	definition.unique = &unique
	index.sqlIndex = &definition
	return stage.Replace(&next)
}
