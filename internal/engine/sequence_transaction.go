package engine

import (
	"context"
	"fmt"
)

// Only sequence DDL and its temporary namespace use this private overlay.
// Original descriptors/name bindings are commit preconditions, not undo data.
type sequenceChanges struct {
	originals   map[ObjectAddress]Descriptor
	descriptors map[ObjectAddress]Descriptor
	names       map[NameKey]ObjectAddress
	generators  map[ObjectAddress]*sequenceGenerator
	cachedBase  *catalogState
	cached      CatalogRead
}

func newSequenceChanges() *sequenceChanges {
	return &sequenceChanges{originals: map[ObjectAddress]Descriptor{}, descriptors: map[ObjectAddress]Descriptor{}, names: map[NameKey]ObjectAddress{}, generators: map[ObjectAddress]*sequenceGenerator{}}
}
func cloneSequenceChanges(c *sequenceChanges) *sequenceChanges {
	n := newSequenceChanges()
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
	return n
}
func relationNameKey(d Descriptor) NameKey {
	return NameKey{ClassID: d.Address().ClassID, Namespace: d.Namespace(), Name: d.Name()}
}

// Caller holds Engine.mu and the session's statement admission.
func (e *Engine) sequenceCatalogLocked(c *sequenceChanges) CatalogRead {
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
func (e *Engine) sequenceChangeLocked(c *sequenceChanges, a ObjectAddress, d Descriptor) {
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
	if s.sequences != nil {
		if g := s.sequences.generators[a]; g != nil {
			return g
		}
	}
	if c := s.owner.sequences; c != nil {
		if g := c.generators[a]; g != nil {
			return g
		}
	}
	return e.sequences[a]
}
func (e *Engine) prepareSequenceCatalog(ctx context.Context, c *sequenceChanges) (*catalogState, error) {
	e.mu.Lock()
	view := e.sequenceCatalogLocked(c)
	e.mu.Unlock()
	for a, d := range c.descriptors {
		if d == nil {
			continue
		}
		p := e.providers[a.ClassID]
		if p == nil {
			return nil, fmt.Errorf("stuntgres: unregistered sequence catalog class")
		}
		if err := p.Validate(view, d); err != nil {
			return nil, err
		}
	}
	return view.state, ctx.Err()
}
func (e *Engine) sequencePreflightLocked(c *sequenceChanges) error {
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

