package core

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
)

// validatePublishedCatalog checks next, the stage applied to the published
// state current, before it is published.
//
// Type providers: every descriptor names the current generation of the types
// it uses. current was valid when published, so only staged descriptors can
// name a stale generation, unless the stage replaces a type that already
// exists (or alters a domain): then every descriptor is checked. A type the
// stage adds has no users outside the stage.
//
// Stored definitions (inheritance, policies, rules, triggers, views, CHECK,
// defaults, generated columns) belong to relations and are checked for every
// relation.
func validatePublishedCatalog(read catalog.Read, current, next *catalog.State, s *CatalogStage) error {
	typeReplaced := len(s.DomainTargets) != 0
	staged := make([]ids.ObjectAddress, 0, s.DescriptorCount())
	for address, d := range s.Descriptors() {
		if d == nil {
			continue
		}
		staged = append(staged, address)
		if t, ok := d.(*catalog.Type); ok && t.SQLType.IsUserDefined() && current.Get(address) != nil {
			typeReplaced = true
		}
	}
	if typeReplaced {
		for _, descriptor := range next.Descriptors() {
			if err := validateTypeUsers(read, descriptor); err != nil {
				return err
			}
		}
	} else {
		// Sorted so that the first failure does not depend on map order.
		slices.SortFunc(staged, CompareDefinitionAddress)
		for _, address := range staged {
			if descriptor := next.Get(address); descriptor != nil {
				if err := validateTypeUsers(read, descriptor); err != nil {
					return err
				}
			}
		}
	}
	for _, relation := range next.Relations() {
		if err := validateStoredDefinitions(read, relation); err != nil {
			return err
		}
	}
	return nil
}

func validateTypeUsers(read catalog.Read, descriptor catalog.Descriptor) error {
	if relation, ok := descriptor.(*catalog.Relation); ok {
		if err := typedef.ValidateTypedAttachment(read, relation); err != nil {
			return err
		}
	}
	return stored.ValidateTypeProviders(read, descriptor)
}

func validateStoredDefinitions(read catalog.Read, relation *catalog.Relation) error {
	if relation.Inheritance != nil {
		if err := table.ValidateInheritance(read, relation); err != nil {
			return err
		}
	}
	if len(relation.Security.Policies) != 0 {
		if err := view.ValidatePolicies(read, relation); err != nil {
			return err
		}
	}
	if len(stored.RulesOf(relation)) != 0 {
		if err := stored.ValidateRules(read, relation); err != nil {
			return err
		}
	}
	if relation.HasTriggers() {
		if err := stored.ValidateTriggers(read, relation); err != nil {
			return err
		}
	}
	if relation.View != nil {
		if err := view.ValidateViewDefinition(read, relation); err != nil {
			return err
		}
	}
	if len(relation.Constraints.Check) != 0 {
		if err := table.ValidateCheckProviders(read, relation); err != nil {
			return err
		}
	}
	if len(relation.ColumnMeta.Defaults) != 0 {
		if err := table.ValidateDefaultProviders(read, relation); err != nil {
			return err
		}
	}
	if len(relation.ColumnMeta.Generations) != 0 {
		if err := table.ValidateGenerationProviders(read, relation); err != nil {
			return err
		}
	}
	return nil
}
