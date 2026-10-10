package engine

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/parser"
)

type defaultDependencyEdge struct {
	relation  *relationDescriptor
	attribute int32
	column    string
	target    ObjectAddress
}

func defaultDepends(d *relationDescriptor, target ObjectAddress) bool {
	for _, definition := range d.defaults {
		for _, dependency := range definition.dependencies {
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
func validateDefaultProviders(view CatalogRead, d *relationDescriptor) error {
	for _, definition := range d.defaults {
		for _, ref := range definition.dependencies {
			address := ref.Address
			address.SubID = 0
			provider, ok := view.Descriptor(address)
			if !ok || !storedReferenceEpochValid(view, d, ref) {
				return fmt.Errorf("stuntgres: missing default provider dependency")
			}
			if relation, ok := provider.(*relationDescriptor); ok {
				if ref.Address.SubID != 0 {
					return fmt.Errorf("stuntgres: invalid default relation provider")
				}
				namespace := view.state.descriptors[relation.namespace].(*namespaceDescriptor)
				if namespace.temporary != (SessionToken{}) {
					owner := view.state.descriptors[d.namespace].(*namespaceDescriptor)
					if owner.temporary != namespace.temporary {
						return fmt.Errorf("stuntgres: foreign temporary default provider")
					}
				}
			} else if provider.Kind() != TypeObject {
				return fmt.Errorf("stuntgres: invalid default provider kind")
			}
		}
	}
	return nil
}

func stageDefaultDependents(stage *CatalogStage, targets []ObjectAddress, mode parser.DropMode) ([]defaultDependencyEdge, error) {
	var edges []defaultDependencyEdge
	for address, descriptor := range stage.base.state.descriptors {
		d, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		if replacement, ok := stage.descriptors[address].(*relationDescriptor); ok {
			d = replacement
		}
		if _, removed := stage.removals[address]; removed {
			continue
		}
		var removed []int32
		for slot, attribute := range d.attributes {
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
			definition, ok := d.defaults[attribute]
			if !ok {
				continue
			}
			var dependency ObjectAddress
			for _, ref := range definition.dependencies {
				for _, target := range targets {
					a := ref.Address
					if a.ClassID == target.ClassID && a.ObjectID == target.ObjectID && (target.SubID == 0 || a.SubID == target.SubID) {
						dependency = target
						break
					}
				}
				if dependency != (ObjectAddress{}) {
					break
				}
			}
			if dependency == (ObjectAddress{}) {
				continue
			}
			edges = append(edges, defaultDependencyEdge{d, attribute, d.columns[slot].Name, dependency})
			removed = append(removed, attribute)
		}
		if len(removed) != 0 && mode != parser.DropRestrict {
			copy := *d
			revision, err := stage.cascadeMetadataRevision(copy.revision)
			if err != nil {
				return nil, err
			}
			copy.revision = revision
			copy.defaults = maps.Clone(d.defaults)
			for _, attribute := range removed {
				delete(copy.defaults, attribute)
			}
			if err := stage.Replace(&copy); err != nil {
				return nil, err
			}
		}
	}
	slices.SortFunc(edges, func(a, b defaultDependencyEdge) int {
		if order := cmp.Compare(a.relation.address.ObjectID, b.relation.address.ObjectID); order != 0 {
			return order
		}
		return cmp.Compare(a.attribute, b.attribute)
	})
	return edges, nil
}
