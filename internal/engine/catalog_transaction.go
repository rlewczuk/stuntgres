package engine

import (
	"context"
	"fmt"
)

// Transactional descriptors, row shapes and generators share one private overlay.
// Original descriptors/name bindings are commit preconditions, not undo data.
type catalogChanges struct {
	originals    map[ObjectAddress]Descriptor
	descriptors  map[ObjectAddress]Descriptor
	names        map[NameKey]ObjectAddress
	generators   map[ObjectAddress]*sequenceGenerator
	storage      map[ObjectAddress]catalogStorageChange
	dependencies []catalogDependencyObservation
	cachedBase   *catalogState
	cached       CatalogRead
}

func newCatalogChanges() *catalogChanges {
	return &catalogChanges{originals: map[ObjectAddress]Descriptor{}, descriptors: map[ObjectAddress]Descriptor{}, names: map[NameKey]ObjectAddress{}, generators: map[ObjectAddress]*sequenceGenerator{}, storage: map[ObjectAddress]catalogStorageChange{}}
}
func cloneCatalogChanges(c *catalogChanges) *catalogChanges {
	n := newCatalogChanges()
	if c == nil {
		return n
	}
	for a, d := range c.originals {
		n.originals[a] = d
	}
	for a, d := range c.descriptors {
		n.descriptors[a] = d
	}
	for k, a := range c.names {
		n.names[k] = a
	}
	for a, g := range c.generators {
		n.generators[a] = g
	}
	for a, shape := range c.storage {
		n.storage[a] = shape
	}
	n.dependencies = append(n.dependencies, c.dependencies...)
	return n
}
func relationNameKey(d Descriptor) NameKey {
	return NameKey{ClassID: d.Address().ClassID, Namespace: d.Namespace(), Name: d.Name()}
}

// Caller holds Engine.mu and the session's statement admission.
func (e *Engine) privateCatalogLocked(c *catalogChanges) CatalogRead {
	if c == nil {
		return CatalogRead{e, e.state}
	}
	if c.cachedBase == e.state && c.cached.state != nil {
		return c.cached
	}
	n := cloneCatalog(e.state)
	for a, d := range c.descriptors {
		if old := n.descriptors[a]; old != nil {
			delete(n.names, relationNameKey(old))
		}
		if d == nil {
			delete(n.descriptors, a)
		} else {
			n.descriptors[a] = d
		}
	}
	for _, d := range c.descriptors {
		if d != nil {
			n.names[relationNameKey(d)] = d.Address()
		}
	}
	c.cachedBase, c.cached = e.state, CatalogRead{e, n}
	return c.cached
}
func (e *Engine) catalogChangeLocked(c *catalogChanges, a ObjectAddress, d Descriptor) {
	if _, ok := c.originals[a]; !ok {
		c.originals[a] = e.state.descriptors[a]
	}
	if original := c.originals[a]; original != nil {
		k := relationNameKey(original)
		if _, ok := c.names[k]; !ok {
			c.names[k] = e.state.names[k]
		}
	}
	if d != nil {
		k := relationNameKey(d)
		if _, ok := c.names[k]; !ok {
			c.names[k] = e.state.names[k]
		}
	}
	c.descriptors[a] = d
	c.cachedBase = nil
	c.cached = CatalogRead{}
}
func (e *Engine) sequenceGeneratorLocked(s *StatementScope, a ObjectAddress) *sequenceGenerator {
	if s.catalog != nil {
		if descriptor, exists := s.catalog.descriptors[a]; exists && descriptor == nil {
			return nil
		}
		if g := s.catalog.generators[a]; g != nil {
			return g
		}
	}
	if c := s.owner.catalog; c != nil {
		if descriptor, exists := c.descriptors[a]; exists && descriptor == nil {
			return nil
		}
		if g := c.generators[a]; g != nil {
			return g
		}
	}
	return e.sequences[a]
}
func (e *Engine) preparePrivateCatalog(ctx context.Context, c *catalogChanges, epoch CatalogEpoch) (*catalogState, error) {
	e.mu.Lock()
	// Bind the overlay only to its owning incarnation. Replacement types must
	// never validate old descriptors before the publication epoch fence.
	if e.state.epoch != epoch {
		e.mu.Unlock()
		return nil, ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(c)
	e.mu.Unlock()
	for a, d := range c.descriptors {
		if d == nil {
			continue
		}
		p := e.providers[a.ClassID]
		if p == nil {
			return nil, fmt.Errorf("stuntgres: unregistered catalog class")
		}
		if err := p.Validate(view, d); err != nil {
			return nil, err
		}
	}
	return view.state, ctx.Err()
}
func (e *Engine) catalogPreflightLocked(c *catalogChanges) error {
	for _, observation := range c.dependencies {
		for a, descriptor := range e.state.descriptors {
			if descriptor == observation.base.descriptors[a] {
				continue
			}
			relation, ok := descriptor.(*relationDescriptor)
			if !ok {
				continue
			}
			for _, target := range observation.targets {
				if viewDepends(relation, target) || checkDepends(relation, target) {
					return CatalogConflict{}
				}
				if relation.sequence != nil && relation.sequence.owned == target {
					// Identity TYPE refreshed this exact internal link after its
					// SRX grant. Options/state revisions are not a new dependency.
					// The original fence below and reciprocal provider validation
					// still reject any change after that authoritative capture.
					original := c.originals[a]
					if relation.sequence.identity && original != nil && original == descriptor {
						continue
					}
					return CatalogConflict{}
				}
			}
		}
	}
	for a, shape := range c.storage {
		store := e.storage[a]
		if store != shape.original || store.generation != shape.generation || len(store.versions) != len(shape.root) || store.ends != shape.ends || !sameVersionRoot(store.versions, shape.root) {
			return CatalogConflict{}
		}
	}
	for a, original := range c.originals {
		if e.state.descriptors[a] != original {
			return CatalogConflict{}
		}
	}
	for k, a := range c.names {
		if e.state.names[k] != a {
			return CatalogConflict{}
		}
	}
	for _, d := range c.descriptors {
		if d != nil {
			if err := validateACL(RoleRead{e, e.roles}, d); err != nil {
				return err
			}
		}
	}
	return nil
}
