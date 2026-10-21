package engine

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

type defaultDependencyEdge struct {
	relation  *catalog.Relation
	attribute int32
	column    string
	target    ids.ObjectAddress
}

func defaultDepends(d *catalog.Relation, target ids.ObjectAddress) bool {
	for _, definition := range d.ColumnMeta.Defaults {
		for _, dependency := range definition.Dependencies {
			a := dependency.Address
			if a.ClassID == target.ClassID && a.ObjectID == target.ObjectID && (target.SubID == 0 || a.SubID == target.SubID) {
				return true
			}
		}
	}
	return false
}

// Validate only captured provider authority against the merged publication root.
// Full expression/dependency agreement is already checked on the private stage.
func validateDefaultProviders(view catalog.Read, d *catalog.Relation) error {
	for _, definition := range d.ColumnMeta.Defaults {
		if err := validateColumnDefaultProviders(view, d, definition); err != nil {
			return err
		}
	}
	return nil
}

func validateColumnDefaultProviders(view catalog.Read, d *catalog.Relation, definition catalog.ColumnDefault) error {
	for _, ref := range definition.Dependencies {
		address := ref.Address
		address.SubID = 0
		provider, ok := view.Descriptor(address)
		if !ok || !stored.ReferenceEpochValid(view, d, ref) {
			return fmt.Errorf("stuntgres: missing default provider dependency")
		}
		if relation, ok := provider.(*catalog.Relation); ok {
			if ref.Address.SubID != 0 {
				return fmt.Errorf("stuntgres: invalid default relation provider")
			}
			namespace := view.MustNamespace(relation.Namespace())
			if namespace.Temporary != (ids.SessionToken{}) {
				owner := view.MustNamespace(d.Namespace())
				if owner.Temporary != namespace.Temporary {
					return fmt.Errorf("stuntgres: foreign temporary default provider")
				}
			}
		} else if provider.Kind() == ids.CollationObject {
			if err := stored.ValidateCollationDependency(view, ref, definition.Expression); err != nil {
				return err
			}
		} else if provider.Kind() == ids.NamespaceObject {
			if ref.Address.SubID != 0 {
				return fmt.Errorf("stuntgres: invalid default namespace provider")
			}
		} else if provider.Kind() != ids.TypeObject && provider.Kind() != ids.ProcedureObject && provider.Kind() != ids.OperatorObject && provider.Kind() != ids.TSDictionaryObject {
			return fmt.Errorf("stuntgres: invalid default provider kind")
		}
	}
	return nil
}

func stageDefaultDependents(stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode) ([]defaultDependencyEdge, error) {
	return defaultDependents(stageDropProvider(context.Background(), stage, nil, nil, nil), targets, mode)
}

func defaultDependents(provider dropDependencyReader, targets []ids.ObjectAddress, mode parser.DropMode) ([]defaultDependencyEdge, error) {
	var edges []defaultDependencyEdge
	for address, descriptor := range provider.inputDescriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		if replacement, ok := provider.replacement(address).(*catalog.Relation); ok {
			d = replacement
		}
		if provider.removed(address) {
			continue
		}
		var removed []int32
		for slot, attribute := range d.Attributes {
			owner := address
			owner.SubID = attribute
			dropping := false
			for _, target := range targets {
				if target == address || target == owner {
					dropping = true
					break
				}
			}
			if dropping {
				continue
			}
			definition, ok := d.ColumnMeta.Defaults[attribute]
			if !ok {
				continue
			}
			var dependency ids.ObjectAddress
			for _, ref := range definition.Dependencies {
				for _, target := range targets {
					a := ref.Address
					if a.ClassID == target.ClassID && a.ObjectID == target.ObjectID && (target.SubID == 0 || a.SubID == target.SubID) {
						dependency = target
						break
					}
				}
				if dependency != (ids.ObjectAddress{}) {
					break
				}
			}
			if dependency == (ids.ObjectAddress{}) {
				continue
			}
			edges = append(edges, defaultDependencyEdge{d, attribute, d.Columns[slot].Name, dependency})
			removed = append(removed, attribute)
		}
		if len(removed) != 0 && mode != parser.DropRestrict {
			if err := provider.inheritanceAlterGuard(d); err != nil {
				return nil, err
			}
			copy := *d
			revision, err := provider.revision(copy.Revision())
			if err != nil {
				return nil, err
			}
			copy.ObjectRevision = revision
			copy.ColumnMeta.Defaults = maps.Clone(d.ColumnMeta.Defaults)
			for _, attribute := range removed {
				delete(copy.ColumnMeta.Defaults, attribute)
			}
			if err := provider.replace(&copy); err != nil {
				return nil, err
			}
		}
	}
	slices.SortFunc(edges, func(a, b defaultDependencyEdge) int {
		if order := cmp.Compare(a.relation.Address().ObjectID, b.relation.Address().ObjectID); order != 0 {
			return order
		}
		return cmp.Compare(a.attribute, b.attribute)
	})
	return edges, nil
}
