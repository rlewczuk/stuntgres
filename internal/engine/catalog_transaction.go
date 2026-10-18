package engine

import (
	"context"
	"fmt"
	"maps"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Transactional descriptors, row shapes and generators share one private overlay.
// Original descriptors/name bindings are commit preconditions, not undo data.
type catalogChanges struct {
	originals           map[ids.ObjectAddress]Descriptor
	descriptors         map[ids.ObjectAddress]Descriptor
	names               map[ids.NameKey]ids.ObjectAddress
	defaultACLs         map[defaultACLKey]*defaultACLRecord
	defaultOriginals    map[defaultACLKey]*defaultACLRecord
	defaultReads        map[defaultACLKey]*defaultACLRecord
	generators          map[ids.ObjectAddress]*sequenceGenerator
	storage             map[ids.ObjectAddress]catalogStorageChange
	uniqueRoots         map[ids.ObjectAddress]*logicalIndexRoot
	indexCandidates     map[ids.ObjectAddress]indexRootCandidate // Private produced DDL roots and their exact baseline pins.
	dependencies        []catalogDependencyObservation
	extensionAuthority  []extensionAuthorityObservation
	cachedBase          *catalogState
	cached              CatalogRead
	enumCreated         map[ids.ObjectAddress]bool // CREATE at top transaction level only.
	enumUnsafe          map[int64]bool             // Globally nonreused member identities.
	rolesBase           *roleCatalog
	roles               *roleCatalog
	retiredRoles        map[authorization.PrincipalRef]struct{}
	indexRebuilds       map[ids.ObjectAddress]*catalog.Relation
	indexFences         []catalogIndexFence
	namespaceFences     []catalogNamespaceFence
	readFences          []partitionReadFence
	truncateFences      []truncateGraphFence
	materializedSources []materializedSourceFence
	domainValidations   []*domainValidationFence
	domainIndexRoots    map[ids.ObjectAddress]*logicalIndexRoot
	domainChanged       bool
}

func newCatalogChanges() *catalogChanges {
	return &catalogChanges{originals: map[ids.ObjectAddress]Descriptor{}, descriptors: map[ids.ObjectAddress]Descriptor{}, names: map[ids.NameKey]ids.ObjectAddress{}, generators: map[ids.ObjectAddress]*sequenceGenerator{}, storage: map[ids.ObjectAddress]catalogStorageChange{}}
}
func cloneCatalogChanges(c *catalogChanges) *catalogChanges {
	n := newCatalogChanges()
	if c == nil {
		return n
	}
	cloneDefaultChanges(c, n)
	if c.enumCreated != nil {
		n.enumCreated = make(map[ids.ObjectAddress]bool, len(c.enumCreated))
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
		n.uniqueRoots = make(map[ids.ObjectAddress]*logicalIndexRoot, len(c.uniqueRoots))
		for a, root := range c.uniqueRoots {
			n.uniqueRoots[a] = root
		}
	}
	if c.indexCandidates != nil {
		n.indexCandidates = make(map[ids.ObjectAddress]indexRootCandidate, len(c.indexCandidates))
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
		n.indexRebuilds = make(map[ids.ObjectAddress]*catalog.Relation, len(c.indexRebuilds))
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
		n.domainIndexRoots = make(map[ids.ObjectAddress]*logicalIndexRoot, len(c.domainIndexRoots))
		for address, root := range c.domainIndexRoots {
			n.domainIndexRoots[address] = root
		}
	}
	return n
}

// Caller holds Engine.mu and the session's statement admission.
func (e *Engine) privateCatalogLocked(c *catalogChanges) CatalogRead {
	if c == nil {
		return e.catalog.read(e.catalog.state)
	}
	if c.cachedBase == e.catalog.state && c.cached.State() != nil {
		return c.cached
	}
	if len(c.descriptors) == 0 && len(c.defaultACLs) == 0 {
		c.cachedBase = e.catalog.state
		c.cached = e.catalog.read(e.catalog.state)
		return c.cached
	}
	n := catalog.CloneState(e.catalog.state)
	n.OverlayDefaultACLs(e.catalog.state, c.defaultACLs)
	operators := catalog.OperatorIndexEdit{Index: e.catalog.state.OperatorNames()}
	for a, d := range c.descriptors {
		operators.Replace(e.catalog.state.Get(a), d)
		if old := n.Get(a); old != nil {
			n.UnbindName(catalog.DescriptorNameKey(old))
		}
		if d == nil {
			n.RemoveDescriptor(a)
			n.RemoveDumpPriority(a)
		} else {
			n.SetDescriptor(a, d)
		}
	}
	for _, d := range c.descriptors {
		if d != nil {
			n.BindName(catalog.DescriptorNameKey(d), d.Address())
		}
	}
	n.SetOperatorNames(operators.Index)
	c.cachedBase, c.cached = e.catalog.state, e.catalog.read(n)
	return c.cached
}
func (e *Engine) catalogChangeLocked(c *catalogChanges, a ids.ObjectAddress, d Descriptor) {
	if _, ok := c.originals[a]; !ok {
		c.originals[a] = e.catalog.state.Get(a)
	}
	if original := c.originals[a]; original != nil {
		k := catalog.DescriptorNameKey(original)
		if _, ok := c.names[k]; !ok {
			c.names[k] = e.catalog.state.NameAddress(k)
		}
	}
	if d != nil {
		k := catalog.DescriptorNameKey(d)
		if _, ok := c.names[k]; !ok {
			c.names[k] = e.catalog.state.NameAddress(k)
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
func (e *Engine) sequenceGeneratorLocked(s *StatementScope, a ids.ObjectAddress) *sequenceGenerator {
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
func (e *Engine) preparePrivateCatalog(ctx context.Context, c *catalogChanges, epoch ids.CatalogEpoch) (*catalogState, error) {
	e.mu.Lock()
	// Bind the overlay only to its owning incarnation. Replacement types must
	// never validate old descriptors before the publication epoch fence.
	if e.catalog.state.Epoch() != epoch {
		e.mu.Unlock()
		return nil, ids.ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(c)
	if err := catalogDependencyPreflight(e.catalog.state, view.State(), c.dependencies, c.originals); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	e.mu.Unlock()
	descriptors := maps.All(c.descriptors)
	if c.domainChanged {
		descriptors = view.Descriptors()
	}
	for a, d := range descriptors {
		if d == nil {
			continue
		}
		p := e.catalog.Provider(a.ClassID)
		if p == nil {
			return nil, fmt.Errorf("stuntgres: unregistered catalog class")
		}
		if err := p.Validate(view, d); err != nil {
			return nil, err
		}
	}
	return view.State(), ctx.Err()
}
func (e *Engine) catalogPreflightLocked(c *catalogChanges) error {
	if c.rolesBase != nil && e.roles != c.rolesBase {
		return CatalogConflict{}
	}
	if !catalog.DefaultRecordsUnchanged(e.catalog.state, c.defaultOriginals) || !catalog.DefaultRecordsUnchanged(e.catalog.state, c.defaultReads) {
		return CatalogConflict{}
	}
	roles := e.roles
	if c.roles != nil {
		roles = c.roles
	}
	view := e.privateCatalogLocked(c)
	read := e.roleRead(view, roles)
	if err := catalog.ValidateDefaultACLs(read, view); err != nil {
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
			if key.ClassID == ids.ExtensionClassID && original == (ids.ObjectAddress{}) && e.catalog.state.NameAddress(key) != original {
				return extensionNameConflict(key.Name)
			}
		}
	}
	for _, observation := range c.extensionAuthority {
		if e.roles.revision != observation.roles || observation.namespace == nil || observation.database == nil ||
			e.catalog.state.Get(observation.namespace.Address()) != observation.namespace ||
			e.catalog.state.Get(observation.database.Address()) != observation.database {
			return CatalogConflict{}
		}
	}
	if err := catalogDependencyPreflight(e.catalog.state, view.State(), c.dependencies, c.originals); err != nil {
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
		if e.catalog.state.Get(a) != original {
			return CatalogConflict{}
		}
	}
	for k, a := range c.names {
		if e.catalog.state.NameAddress(k) != a {
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
func catalogDependencyPreflight(current, candidate *catalogState, observations []catalogDependencyObservation, originals map[ids.ObjectAddress]Descriptor) error {
	for _, observation := range observations {
		if current == observation.base {
			continue
		}
		for address, descriptor := range current.Descriptors() {
			if descriptor == observation.base.Get(address) {
				continue
			}
			relation, _ := descriptor.(*catalog.Relation)
			owner := candidate.Get(address)
			for _, target := range observation.targets {
				if relation != nil {
					if viewDepends(relation, target) || materializedExtensionDepends(relation, target) || checkDepends(relation, target) || defaultDepends(relation, target) || generationDepends(relation, target) || indexExpressionDepends(relation, target) {
						return CatalogConflict{}
					}
					if relation.Sequence != nil && relation.Sequence.Owned == target {
						// Identity TYPE refreshes this exact internal link under
						// its SRX grant; preserve the existing original fence.
						if !relation.Sequence.Identity || originals[address] != descriptor {
							return CatalogConflict{}
						}
					}
				}
				if candidate.Get(target) != nil {
					continue
				}
				switch owner := owner.(type) {
				case *foreignDescriptor:
					for _, dependency := range foreignDependencyAddresses(owner) {
						if dependency != (ids.ObjectAddress{}) && dependency == target {
							return CatalogConflict{}
						}
					}
				case *catalog.Relation:
					if owner.Foreign != nil && owner.Foreign.Server == target {
						return CatalogConflict{}
					}
				}
			}
		}
	}
	return nil
}
