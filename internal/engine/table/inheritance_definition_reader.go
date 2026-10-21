package table

import (
	"iter"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

// definitionDelta is one prepared provider result the reader overlays (a
// prepared column drop or identity drop). It reports only real effects; the
// reader owns fallback to its base and resolution observations.
type DefinitionDelta interface {
	DefinitionDelta(ids.ObjectAddress) (descriptor catalog.Descriptor, present, handled bool)
	LayoutDelta(ids.ObjectAddress) (index.LayoutLineage, bool)
}

// Each link is an actual provider result, never a reconstructed candidate
// catalog. Readers retain the exact base captured for that cooking invocation.
type inheritanceDefinitionEffect struct {
	previous   *inheritanceDefinitionEffect
	descriptor catalog.Descriptor
	delta      DefinitionDelta
}
type InheritanceDefinitionObservations struct {
	descriptors map[ids.ObjectAddress]catalog.Descriptor
	names       map[ids.NameKey]ids.ObjectAddress
}
type InheritanceDefinitionReader struct {
	Base         catalog.Read
	effects      *inheritanceDefinitionEffect
	Observations *InheritanceDefinitionObservations
}

func (r InheritanceDefinitionReader) observe(address ids.ObjectAddress) {
	if r.Observations == nil {
		return
	}
	if r.Observations.descriptors == nil {
		r.Observations.descriptors = make(map[ids.ObjectAddress]catalog.Descriptor)
	}
	if _, seen := r.Observations.descriptors[address]; !seen {
		r.Observations.descriptors[address] = r.Base.Get(address)
	}
}
func (r InheritanceDefinitionReader) Descriptor(address ids.ObjectAddress) (catalog.Descriptor, bool) {
	r.observe(address)
	for effect := r.effects; effect != nil; effect = effect.previous {
		if effect.descriptor != nil && effect.descriptor.Address() == address {
			return effect.descriptor, true
		}
		if effect.delta != nil {
			if descriptor, present, handled := effect.delta.DefinitionDelta(address); handled {
				return descriptor, present
			}
		}
	}
	return r.Base.Descriptor(address)
}
func (r InheritanceDefinitionReader) Lookup(key ids.NameKey) ids.ObjectAddress {
	address := r.Base.NameAddress(key)
	if r.Observations != nil {
		if r.Observations.names == nil {
			r.Observations.names = make(map[ids.NameKey]ids.ObjectAddress)
		}
		if _, seen := r.Observations.names[key]; !seen {
			r.Observations.names[key] = address
		}
	}
	for effect := r.effects; effect != nil; effect = effect.previous {
		if effect.descriptor != nil && catalog.DescriptorNameKey(effect.descriptor) == key {
			descriptor, present := r.Descriptor(effect.descriptor.Address())
			if present && catalog.DescriptorNameKey(descriptor) == key {
				return descriptor.Address()
			}
		}
	}
	descriptor, present := r.Descriptor(address)
	if present && catalog.DescriptorNameKey(descriptor) == key {
		return address
	}
	return ids.ObjectAddress{}
}
func (r InheritanceDefinitionReader) Descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	// Enumeration alone does not authorize a new global invalidation fence. The
	// selected provider's ordinary binding records its exact dependency identity.
	r.Observations = nil
	return func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		for address := range r.Base.Descriptors() {
			if descriptor, present := r.Descriptor(address); present && !yield(address, descriptor) {
				return
			}
		}
	}
}

func (r InheritanceDefinitionReader) ProceduresNamed(namespace ids.ObjectAddress, name string) iter.Seq[*catalog.Procedure] {
	// As with full discovery, candidate enumeration does not create observations.
	r.Observations = nil
	return catalog.IndexedDefinitionProcedures(r.Base, r, namespace, name)
}
func (r InheritanceDefinitionReader) HasSQLProcedureName(name string) bool {
	return r.Base.State().HasSQLProcedureName(name)
}
func (r InheritanceDefinitionReader) IndexLayout(address ids.ObjectAddress) (index.LayoutLineage, bool) {
	for effect := r.effects; effect != nil; effect = effect.previous {
		if effect.delta != nil {
			if layout, present := effect.delta.LayoutDelta(address); present {
				return layout, true
			}
		}
	}
	return index.LayoutLineage{}, false
}
func (r *InheritanceDefinitionReader) AppendDescriptor(descriptor catalog.Descriptor) {
	r.effects = &inheritanceDefinitionEffect{previous: r.effects, descriptor: descriptor}
}
func (r *InheritanceDefinitionReader) AppendDelta(delta DefinitionDelta) {
	r.effects = &inheritanceDefinitionEffect{previous: r.effects, delta: delta}
}
func (r InheritanceDefinitionReader) Validate(view catalog.Read) error {
	if view.Owner() != r.Base.Owner() || view.Epoch() != r.Base.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if r.Observations == nil {
		return nil
	}
	for address, descriptor := range r.Observations.descriptors {
		if view.Get(address) != descriptor {
			return ids.ErrCatalogReplaced
		}
	}
	for key, address := range r.Observations.names {
		if view.NameAddress(key) != address {
			return ids.ErrCatalogReplaced
		}
	}
	return nil
}
func (state *InheritanceAlterPlan) DefinitionRelation(view catalog.Read, address ids.ObjectAddress) *catalog.Relation {
	if state.Preparing {
		descriptor, _ := state.Definition.Descriptor(address)
		relation, _ := descriptor.(*catalog.Relation)
		return relation
	}
	relation, _ := view.Relation(address)
	return relation
}
func InheritanceMarkerPublication(old, current *catalog.Relation) bool {
	return old == current || current != nil && old != nil && !old.History.HasSubclasses && current.History.HasSubclasses && current.History.SubclassPredecessor == old && current.Revision() == current.History.SubclassPublication
}

// DiscoveryReader reads like r but records no observations, so dependency
// discovery does not widen the node fence.
func (r InheritanceDefinitionReader) DiscoveryReader() catalog.DefinitionReader {
	r.Observations = nil
	return r
}
