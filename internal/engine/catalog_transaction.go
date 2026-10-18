package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
)

// Transactional descriptors, row shapes and generators share one private overlay.
// Original descriptors/name bindings are commit preconditions, not undo data.
type catalogChanges struct {
	originals           map[ObjectAddress]Descriptor
	descriptors         map[ObjectAddress]Descriptor
	names               map[NameKey]ObjectAddress
	defaultACLs         map[defaultACLKey]*defaultACLRecord
	defaultOriginals    map[defaultACLKey]*defaultACLRecord
	defaultReads        map[defaultACLKey]*defaultACLRecord
	generators          map[ObjectAddress]*sequenceGenerator
	storage             map[ObjectAddress]catalogStorageChange
	uniqueRoots         map[ObjectAddress]*logicalIndexRoot
	indexCandidates     map[ObjectAddress]indexRootCandidate // Private produced DDL roots and their exact baseline pins.
	dependencies        []catalogDependencyObservation
	extensionAuthority  []extensionAuthorityObservation
	cachedBase          *catalogState
	cached              CatalogRead
	enumCreated         map[ObjectAddress]bool // CREATE at top transaction level only.
	enumUnsafe          map[int64]bool         // Globally nonreused member identities.
	rolesBase           *roleCatalog
	roles               *roleCatalog
	retiredRoles        map[authorization.PrincipalRef]struct{}
	indexRebuilds       map[ObjectAddress]*relationDescriptor
	indexFences         []catalogIndexFence
	namespaceFences     []catalogNamespaceFence
	readFences          []partitionReadFence
	truncateFences      []truncateGraphFence
	materializedSources []materializedSourceFence
	domainValidations   []*domainValidationFence
	domainIndexRoots    map[ObjectAddress]*logicalIndexRoot
	domainChanged       bool
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
	if c.indexCandidates != nil {
		n.indexCandidates = make(map[ObjectAddress]indexRootCandidate, len(c.indexCandidates))
		for a, candidate := range c.indexCandidates {
			n.indexCandidates[a] = candidate
		}
	}
	n.dependencies = append(n.dependencies, c.dependencies...)
	n.materializedSources = append(n.materializedSources, c.materializedSources...)
	n.rolesBase, n.roles = c.rolesBase, c.roles
	if len(c.retiredRoles) != 0 {
		n.retiredRoles = make(map[authorization.PrincipalRef]struct{}, len(c.retiredRoles))
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
	n.domainValidations = append(n.domainValidations, c.domainValidations...)
	if c.domainIndexRoots != nil {
		n.domainIndexRoots = make(map[ObjectAddress]*logicalIndexRoot, len(c.domainIndexRoots))
		for address, root := range c.domainIndexRoots {
			n.domainIndexRoots[address] = root
		}
	}
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
	operators := operatorIndexEdit{index: e.state.operatorNames}
	for a, d := range c.descriptors {
		operators.replace(e.state.descriptors[a], d)
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
	n.operatorNames = operators.index
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
		delete(c.indexCandidates, a)
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
	if err := catalogDependencyPreflight(e.state, view.state, c.dependencies, c.originals); err != nil {
		e.mu.Unlock()
		return nil, err
	}
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
	catalog := e.privateCatalogLocked(c)
	read := e.roleRead(catalog, roles)
	if err := validateDefaultACLs(read, catalog); err != nil {
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
	if err := catalogDependencyPreflight(e.state, catalog.state, c.dependencies, c.originals); err != nil {
		return err
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
	for _, fence := range c.materializedSources {
		if !e.materializedSourceFenceCurrent(fence) {
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

// Inspect only committed descriptors changed since a captured retirement.
// Candidate overrides matter: an owner removed or rewired by this transaction
// must not impersonate a surviving edge into an absent provider.
func catalogDependencyPreflight(current, candidate *catalogState, observations []catalogDependencyObservation, originals map[ObjectAddress]Descriptor) error {
	for _, observation := range observations {
		if current == observation.base {
			continue
		}
		for address, descriptor := range current.descriptors {
			if descriptor == observation.base.descriptors[address] {
				continue
			}
			relation, _ := descriptor.(*relationDescriptor)
			owner := candidate.descriptors[address]
			for _, target := range observation.targets {
				if relation != nil {
					if viewDepends(relation, target) || materializedExtensionDepends(relation, target) || checkDepends(relation, target) || defaultDepends(relation, target) || generationDepends(relation, target) || indexExpressionDepends(relation, target) {
						return CatalogConflict{}
					}
					if relation.sequence != nil && relation.sequence.owned == target {
						// Identity TYPE refreshes this exact internal link under
						// its SRX grant; preserve the existing original fence.
						if !relation.sequence.identity || originals[address] != descriptor {
							return CatalogConflict{}
						}
					}
				}
				if candidate.descriptors[target] != nil {
					continue
				}
				switch owner := owner.(type) {
				case *foreignDescriptor:
					for _, dependency := range foreignDependencyAddresses(owner) {
						if dependency != (ObjectAddress{}) && dependency == target {
							return CatalogConflict{}
						}
					}
				case *relationDescriptor:
					if owner.foreign != nil && owner.foreign.server == target {
						return CatalogConflict{}
					}
				}
			}
		}
	}
	return nil
}
