package engine

import (
	"iter"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Each link is an actual provider result, never a reconstructed candidate
// catalog. Readers retain the exact base captured for that cooking invocation.
type inheritanceDefinitionEffect struct {
	previous   *inheritanceDefinitionEffect
	descriptor catalog.Descriptor
	identity   *preparedIdentityDrop
	drop       *preparedDrop
}
type inheritanceDefinitionObservations struct {
	descriptors map[ids.ObjectAddress]catalog.Descriptor
	names       map[ids.NameKey]ids.ObjectAddress
}
type inheritanceDefinitionReader struct {
	base         catalog.Read
	effects      *inheritanceDefinitionEffect
	observations *inheritanceDefinitionObservations
}

func (r inheritanceDefinitionReader) observe(address ids.ObjectAddress) {
	if r.observations == nil {
		return
	}
	if r.observations.descriptors == nil {
		r.observations.descriptors = make(map[ids.ObjectAddress]catalog.Descriptor)
	}
	if _, seen := r.observations.descriptors[address]; !seen {
		r.observations.descriptors[address] = r.base.Get(address)
	}
}
func (r inheritanceDefinitionReader) descriptor(address ids.ObjectAddress) (catalog.Descriptor, bool) {
	r.observe(address)
	for effect := r.effects; effect != nil; effect = effect.previous {
		if effect.descriptor != nil && effect.descriptor.Address() == address {
			return effect.descriptor, true
		}
		if effect.identity != nil {
			if descriptor, present, handled := effect.identity.delta(address); handled {
				return descriptor, present
			}
		}
		if effect.drop != nil {
			if slot := effect.drop.slots[address]; slot.descriptor != 0 {
				delta := effect.drop.effects[slot.descriptor-1]
				if slot.removed {
					return nil, false
				}
				if delta.after != nil {
					return delta.after, true
				}
				return delta.descriptor, delta.descriptor != nil
			}
		}
	}
	return r.base.Descriptor(address)
}
func (r inheritanceDefinitionReader) lookup(key ids.NameKey) ids.ObjectAddress {
	address := r.base.NameAddress(key)
	if r.observations != nil {
		if r.observations.names == nil {
			r.observations.names = make(map[ids.NameKey]ids.ObjectAddress)
		}
		if _, seen := r.observations.names[key]; !seen {
			r.observations.names[key] = address
		}
	}
	for effect := r.effects; effect != nil; effect = effect.previous {
		if effect.descriptor != nil && catalog.DescriptorNameKey(effect.descriptor) == key {
			descriptor, present := r.descriptor(effect.descriptor.Address())
			if present && catalog.DescriptorNameKey(descriptor) == key {
				return descriptor.Address()
			}
		}
	}
	descriptor, present := r.descriptor(address)
	if present && catalog.DescriptorNameKey(descriptor) == key {
		return address
	}
	return ids.ObjectAddress{}
}
func (r inheritanceDefinitionReader) descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	// Enumeration alone does not authorize a new global invalidation fence. The
	// selected provider's ordinary binding records its exact dependency identity.
	r.observations = nil
	return func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		for address := range r.base.Descriptors() {
			if descriptor, present := r.descriptor(address); present && !yield(address, descriptor) {
				return
			}
		}
	}
}
func (r inheritanceDefinitionReader) indexLayout(address ids.ObjectAddress) (indexLayoutLineage, bool) {
	for effect := r.effects; effect != nil; effect = effect.previous {
		if effect.drop != nil {
			if slot := effect.drop.slots[address]; slot.layout != 0 {
				return *effect.drop.effects[slot.layout-1].layout, true
			}
		}
		if effect.identity != nil {
			if layout, present := effect.identity.indexLayout(address); present {
				return layout, true
			}
		}
	}
	return indexLayoutLineage{}, false
}
func (r *inheritanceDefinitionReader) appendDescriptor(descriptor catalog.Descriptor) {
	r.effects = &inheritanceDefinitionEffect{previous: r.effects, descriptor: descriptor}
}
func (r *inheritanceDefinitionReader) appendDrop(drop *preparedDrop) {
	r.effects = &inheritanceDefinitionEffect{previous: r.effects, drop: drop}
}
func (r *inheritanceDefinitionReader) appendIdentity(identity *preparedIdentityDrop) {
	r.effects = &inheritanceDefinitionEffect{previous: r.effects, identity: identity}
}
func (r inheritanceDefinitionReader) validate(view catalog.Read) error {
	if view.Owner() != r.base.Owner() || view.Epoch() != r.base.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if r.observations == nil {
		return nil
	}
	for address, descriptor := range r.observations.descriptors {
		if view.Get(address) != descriptor {
			return ids.ErrCatalogReplaced
		}
	}
	for key, address := range r.observations.names {
		if view.NameAddress(key) != address {
			return ids.ErrCatalogReplaced
		}
	}
	return nil
}
func (state *inheritanceAlterState) definitionRelation(view catalog.Read, address ids.ObjectAddress) *catalog.Relation {
	if state.preparing {
		descriptor, _ := state.definition.descriptor(address)
		relation, _ := descriptor.(*catalog.Relation)
		return relation
	}
	relation, _ := view.Relation(address)
	return relation
}
func inheritanceMarkerPublication(old, current *catalog.Relation) bool {
	return old == current || current != nil && old != nil && !old.History.HasSubclasses && current.History.HasSubclasses && current.History.SubclassPredecessor == old && current.Revision() == current.History.SubclassPublication
}

func (r inheritanceDefinitionReader) discoveryReader() definitionReader {
	r.observations = nil
	return r
}
