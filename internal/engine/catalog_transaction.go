package engine

import (
	"context"
	"fmt"
)

// Transactional descriptors, row shapes and generators share one private overlay.
// Original descriptors/name bindings are commit preconditions, not undo data.
type catalogChanges struct {
	originals          map[ObjectAddress]Descriptor
	descriptors        map[ObjectAddress]Descriptor
	names              map[NameKey]ObjectAddress
	defaultACLs        map[defaultACLKey]*defaultACLRecord
	defaultOriginals   map[defaultACLKey]*defaultACLRecord
	defaultReads       map[defaultACLKey]*defaultACLRecord
	generators         map[ObjectAddress]*sequenceGenerator
	storage            map[ObjectAddress]catalogStorageChange
	uniqueRoots        map[ObjectAddress]*logicalIndexRoot
	dependencies       []catalogDependencyObservation
	extensionAuthority []extensionAuthorityObservation
	cachedBase         *catalogState
	cached             CatalogRead
	enumCreated        map[ObjectAddress]bool // CREATE at top transaction level only.
	enumUnsafe         map[int64]bool         // Globally nonreused member identities.
	rolesBase          *roleCatalog
	roles              *roleCatalog
	retiredRoles       map[PrincipalRef]struct{}
	indexRebuilds      map[ObjectAddress]*relationDescriptor
	indexFences        []catalogIndexFence
	namespaceFences    []catalogNamespaceFence
	readFences         []partitionReadFence
	truncateFences     []truncateGraphFence
	domainChanged      bool
}

func newCatalogChanges() *catalogChanges {
	return &catalogChanges{originals: map[ObjectAddress]Descriptor{}, descriptors: map[ObjectAddress]Descriptor{}, names: map[NameKey]ObjectAddress{}, generators: map[ObjectAddress]*sequenceGenerator{}, storage: map[ObjectAddress]catalogStorageChange{}}
}
func cloneCatalogChanges(c *catalogChanges) *catalogChanges {
	n := newCatalogChanges()
	if c == nil {
		return n
	}
	cloneDefaultChanges(c, n)
	if c.enumCreated != nil {
		n.enumCreated = make(map[ObjectAddress]bool, len(c.enumCreated))
		for a, v := range c.enumCreated {
			n.enumCreated[a] = v
		}
	}
	if c.enumUnsafe != nil {
		n.enumUnsafe = make(map[int64]bool, len(c.enumUnsafe))
		for id, v := range c.enumUnsafe {
			n.enumUnsafe[id] = v
		}
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
	if c.uniqueRoots != nil {
		n.uniqueRoots = make(map[ObjectAddress]*logicalIndexRoot, len(c.uniqueRoots))
		for a, root := range c.uniqueRoots {
			n.uniqueRoots[a] = root
		}
	}
	n.dependencies = append(n.dependencies, c.dependencies...)
	n.rolesBase, n.roles = c.rolesBase, c.roles
	if len(c.retiredRoles) != 0 {
		n.retiredRoles = make(map[PrincipalRef]struct{}, len(c.retiredRoles))
		for ref := range c.retiredRoles {
			n.retiredRoles[ref] = struct{}{}
		}
	}
	n.domainChanged = c.domainChanged
	if c.indexRebuilds != nil {
		n.indexRebuilds = make(map[ObjectAddress]*relationDescriptor, len(c.indexRebuilds))
		for a, d := range c.indexRebuilds {
			n.indexRebuilds[a] = d
		}
	}
	n.indexFences = append(n.indexFences, c.indexFences...)
	n.namespaceFences = append(n.namespaceFences, c.namespaceFences...)
	n.readFences = append(n.readFences, c.readFences...)
	n.truncateFences = append(n.truncateFences, c.truncateFences...)
	n.extensionAuthority = append(n.extensionAuthority, c.extensionAuthority...)
	return n
}

// Caller holds Engine.mu and the session's statement admission.
func (e *Engine) privateCatalogLocked(c *catalogChanges) CatalogRead {
	if c == nil {
		return CatalogRead{e, e.state}
	}
	if c.cachedBase == e.state && c.cached.state != nil {
		return c.cached
	}
	if len(c.descriptors) == 0 && len(c.defaultACLs) == 0 {
		c.cachedBase = e.state
		c.cached = CatalogRead{e, e.state}
		return c.cached
	}
	n := cloneCatalog(e.state)
	n.defaultACLs = overlayDefaultACLs(e.state.defaultACLs, c.defaultACLs)
	for a, d := range c.descriptors {
		if old := n.descriptors[a]; old != nil {
			delete(n.names, descriptorNameKey(old))
		}
		if d == nil {
			delete(n.descriptors, a)
			delete(n.dumpPriorities, a)
		} else {
			n.descriptors[a] = d
		}
	}
	for _, d := range c.descriptors {
		if d != nil {
			n.names[descriptorNameKey(d)] = d.Address()
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
		k := descriptorNameKey(original)
		if _, ok := c.names[k]; !ok {
			c.names[k] = e.state.names[k]
		}
	}
	if d != nil {
		k := descriptorNameKey(d)
		if _, ok := c.names[k]; !ok {
			c.names[k] = e.state.names[k]
		}
	}
	c.descriptors[a] = d
	if d == nil {
		delete(c.uniqueRoots, a)
	}
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
	descriptors := c.descriptors
	if c.domainChanged {
		descriptors = view.state.descriptors
	}
	for a, d := range descriptors {
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
	if c.rolesBase != nil && e.roles != c.rolesBase {
		return CatalogConflict{}
	}
	if !defaultRecordsUnchanged(e.state, c.defaultOriginals) || !defaultRecordsUnchanged(e.state, c.defaultReads) {
		return CatalogConflict{}
	}
	roles := e.roles
	if c.roles != nil {
		roles = c.roles
	}
	read := RoleRead{e, roles, e.privateCatalogLocked(c)}
	if err := validateDefaultACLs(read, read.catalog); err != nil {
		return err
	}
	if err := e.catalogReadPreflightLocked(c); err != nil {
		return err
	}
	// Extension scripts insert the database-wide row before their members.
	// Preserve that public uniqueness diagnostic when a competing installation
	// wins after this script's private snapshot; unrelated catalog fences retain
	// the central publication-conflict contract.
	if len(c.extensionAuthority) != 0 {
		for key, original := range c.names {
			if key.ClassID == ExtensionClassID && original == (ObjectAddress{}) && e.state.names[key] != original {
				return extensionNameConflict(key.Name)
			}
		}
	}
	for _, observation := range c.extensionAuthority {
		if e.roles.revision != observation.roles || observation.namespace == nil || observation.database == nil ||
			e.state.descriptors[observation.namespace.Address()] != observation.namespace ||
			e.state.descriptors[observation.database.Address()] != observation.database {
			return CatalogConflict{}
		}
	}
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
				if viewDepends(relation, target) || materializedExtensionDepends(relation, target) || checkDepends(relation, target) || defaultDepends(relation, target) || generationDepends(relation, target) || indexExpressionDepends(relation, target) {
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
	if err := e.truncateGraphPreflightLocked(c); err != nil {
		return err
	}
	for a, shape := range c.storage {
		store := e.storage[a]
		if store != shape.original || store != nil && (store.generation != shape.generation || len(store.versions) != len(shape.root) || store.ends != shape.ends || !sameVersionRoot(store.versions, shape.root)) {
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
			if err := validateACL(read, d); err != nil {
				return err
			}
		}
	}
	return nil
}
