package engine

import (
	"fmt"

	"stuntgres/internal/engine/ids"
)

type catalogIndexFence struct {
	parent  ids.ObjectAddress
	indexes map[ids.ObjectAddress]bool
}
type catalogNamespaceFence struct {
	namespace ids.ObjectAddress
	members   map[ids.ObjectAddress]Descriptor
}

func (e *Engine) stageCatalogBaseLocked(s *CatalogStage) *catalogState {
	if s.authSession != nil && s.authSession.transactions.retainsTransaction() {
		return e.privateCatalogLocked(s.authSession.transactions.catalog).state
	}
	return e.catalog.state
}
func (e *Engine) stageStorageLocked(s *CatalogStage, a ids.ObjectAddress) *relationStorage {
	if s.authSession != nil && s.authSession.transactions.retainsTransaction() {
		return e.privateStorageLocked(s.authSession.transactions.catalog, a)
	}
	return e.storage[a]
}
func (e *Engine) catalogReadPreflightLocked(c *catalogChanges) error {
	for _, fence := range c.domainValidations {
		if !fence.current(CatalogRead{e, e.catalog.state}, e.storage) {
			return CatalogConflict{}
		}
	}
	for address, root := range c.domainIndexRoots {
		if e.indexRoots[address] != root {
			return CatalogConflict{}
		}
	}
	for _, f := range c.readFences {
		s := e.storage[f.address]
		if s != f.storage || s != nil && (len(s.versions) != f.length || s.ends != f.ends || s.generation != f.generation) {
			return CatalogConflict{}
		}
	}
	for _, f := range c.indexFences {
		count := 0
		for a, d := range e.catalog.state.descriptors {
			r, ok := d.(*relationDescriptor)
			if ok && r.sqlIndex != nil && r.sqlIndex.parent == f.parent {
				if !f.indexes[a] {
					return CatalogConflict{}
				}
				count++
			}
		}
		if count != len(f.indexes) {
			return CatalogConflict{}
		}
	}
	for _, f := range c.namespaceFences {
		count := 0
		for a, d := range e.catalog.state.descriptors {
			if d.Namespace() == f.namespace {
				if f.members[a] != d {
					return CatalogConflict{}
				}
				count++
			}
		}
		if count != len(f.members) {
			return CatalogConflict{}
		}
	}
	return nil
}

// Capture committed predecessor fences while checking the exact private roots
// used by the ordinary stage. Pending writes are not committed observations.
func (e *Engine) captureCatalogStageLocked(s *CatalogStage, c *catalogChanges, current *catalogState, authority *catalogAuthority) error {
	if err := s.defaultPreflight(current); err != nil {
		return err
	}
	if err := e.captureDomainStageLocked(s, c, current); err != nil {
		return err
	}
	if r := s.materializedReplacement; r != nil {
		if err := s.materializedReplacementCurrent(current, e.stageStorageLocked(s, r.original.address)); err != nil {
			return err
		}
	}
	observe := func(a ids.ObjectAddress, d Descriptor) error {
		if current.descriptors[a] != d {
			return CatalogConflict{}
		}
		if _, ok := c.originals[a]; !ok {
			c.originals[a] = e.catalog.state.descriptors[a]
		}
		return nil
	}
	if s.databasePredecessor != nil {
		if current.descriptors[currentDatabaseAddress] != s.databaseAuthority || e.catalog.state.descriptors[currentDatabaseAddress] != s.databasePredecessor {
			return ErrCatalogReplaced
		}
		if original, ok := c.originals[currentDatabaseAddress]; ok && original != s.databasePredecessor {
			return CatalogConflict{}
		}
		c.originals[currentDatabaseAddress] = s.databasePredecessor
		if authority != nil {
			authority.databaseAuthority = s.databaseAuthority
			authority.databasePredecessor = s.databasePredecessor
		}
	}
	for a := range s.descriptors {
		if err := observe(a, s.base.state.descriptors[a]); err != nil {
			return err
		}
	}
	for a := range s.removals {
		if err := observe(a, s.base.state.descriptors[a]); err != nil {
			return err
		}
	}
	observeNamespace := func(ns ids.ObjectAddress) error {
		if d := s.base.state.descriptors[ns]; d != nil {
			// TEMP creation can add its namespace to the candidate before
			// constructing the ordinary stage. That exact creation intent is
			// part of the stage's entry authority, not a later projection.
			// Its committed absence/name fences already belong to c.
			if current.descriptors[ns] == nil {
				original, created := c.originals[ns]
				key := ids.NameKey{ClassID: ids.NamespaceClassID, Name: d.Name()}
				_, named := c.names[key]
				if created && original == nil && c.descriptors[ns] == d && named && current.names[key] == (ids.ObjectAddress{}) {
					return nil
				}
			}
			return observe(ns, d)
		}
		return nil
	}
	for _, d := range s.descriptors {
		if err := observeNamespace(d.Namespace()); err != nil {
			return err
		}
		if old := s.base.state.descriptors[d.Address()]; old != nil {
			if err := observeNamespace(old.Namespace()); err != nil {
				return err
			}
		}
	}
	domainBases := s.domainBases
	if authority != nil && authority.session == s.authSession && authority.authorization == s.authRevision && authority.roles == s.roleRevision &&
		len(domainBases) != 0 && len(authority.observations) == len(domainBases) && &authority.observations[0] == &domainBases[0] {
		// These exact validated providers belong to this statement's Finish
		// authority, not to the outer transaction's predecessor fences.
		domainBases = nil
	}
	for _, source := range s.querySources {
		if !materializedSourceCurrent(s.materializedSourceView, source, current, e.stageStorageLocked(s, source.Address())) {
			return CatalogConflict{}
		}
		if snapshot := s.materializedSourceView; snapshot != nil {
			if relation, ok := source.(*relationDescriptor); ok && relation.relationKind == materializedViewRelation {
				fence := materializedSourceFence{ref: e.catalog.domain.Ref(snapshot.catalog.Epoch(), relation.address), relation: relation, storage: snapshot.generationAt(relation.address)}
				if fence.storage != nil && e.materializedSourceFenceCurrent(fence) && c.descriptors[relation.address] == nil {
					c.materializedSources = append(c.materializedSources, fence)
					continue
				}
			}
		}
		if observed := s.base.state.descriptors[source.Address()]; observed != nil {
			if err := observe(source.Address(), observed); err != nil {
				return err
			}
		}
	}
	for _, sources := range [][]Descriptor{s.likeSources, domainBases} {
		for _, d := range sources {
			observed := s.base.state.descriptors[d.Address()]
			if observed != nil {
				if err := observe(d.Address(), observed); err != nil {
					return err
				}
			}
		}
	}
	for a, d := range s.indexParents {
		if err := observe(a, d); err != nil {
			return err
		}
	}
	for a, d := range s.indexRebuilds {
		if err := observe(a, d); err != nil {
			return err
		}
		if c.indexRebuilds == nil {
			c.indexRebuilds = map[ids.ObjectAddress]*relationDescriptor{}
		}
		c.indexRebuilds[a] = d
	}
	for k, a := range s.touched {
		if current.names[k] != a {
			return CatalogConflict{}
		}
		if _, ok := c.names[k]; !ok {
			c.names[k] = e.catalog.state.names[k]
		}
	}
	for parent := range s.indexSets {
		for address, descriptor := range current.descriptors {
			index, ok := descriptor.(*relationDescriptor)
			if ok && index.sqlIndex != nil && index.sqlIndex.parent == parent {
				if err := observe(address, index); err != nil {
					return err
				}
			}
		}
		f := catalogIndexFence{parent: parent, indexes: map[ids.ObjectAddress]bool{}}
		for a, d := range e.catalog.state.descriptors {
			r, ok := d.(*relationDescriptor)
			if ok && r.sqlIndex != nil && r.sqlIndex.parent == parent {
				f.indexes[a] = true
			}
		}
		c.indexFences = append(c.indexFences, f)
	}
	for a := range s.removals {
		if a.ClassID == ids.NamespaceClassID {
			f := catalogNamespaceFence{namespace: a, members: map[ids.ObjectAddress]Descriptor{}}
			for address, d := range e.catalog.state.descriptors {
				if d.Namespace() == a {
					f.members[address] = d
				}
			}
			c.namespaceFences = append(c.namespaceFences, f)
		}
	}
	captureStorage := func(a ids.ObjectAddress) {
		store := e.storage[a]
		f := partitionReadFence{address: a, storage: store}
		if store != nil {
			f.length = len(store.versions)
			f.ends = store.ends
			f.generation = store.generation
		}
		c.readFences = append(c.readFences, f)
	}
	for _, f := range s.partitionReads {
		captureStorage(f.address)
	}
	for _, d := range s.indexParents {
		captureStorage(d.address)
	}
	for a, candidate := range s.indexCandidates {
		index, ok := s.descriptors[a].(*relationDescriptor)
		if !ok {
			index, ok = current.descriptors[a].(*relationDescriptor)
		}
		if !ok || index.sqlIndex == nil {
			return CatalogConflict{}
		}
		// Candidate data belongs to the actual overlay target, not the
		// committed/private predecessor used by captureStorage's read fence.
		shape, staged := c.storage[index.sqlIndex.parent]
		store := shape.replacement
		if !staged {
			store = e.stageStorageLocked(s, index.sqlIndex.parent)
		}
		if shape, changed := s.rowShapes[index.sqlIndex.parent]; changed {
			// capture precedes stageCatalogChangesLocked: supplied generation
			// roots belong to this stage's destination heap, not c's predecessor.
			store = shape.replacement
		}
		if !candidate.matchesIndex(index, store) {
			return CatalogConflict{}
		}
		captureStorage(index.sqlIndex.parent)
	}
	c.dependencies = append(c.dependencies, s.identityDependencies...)
	if s.authSession != nil && c.rolesBase == nil {
		c.rolesBase = e.roles
	}
	c.domainChanged = c.domainChanged || len(s.domainTargets) != 0
	for _, d := range s.descriptors {
		if typ, ok := d.(*typeDescriptor); ok && typ.typ.IsUserDefined() {
			c.domainChanged = true
			break
		}
	}
	return nil
}

func (e *Engine) stageMessageCatalog(op *Operation, p *PreparedCatalogPublication, token PublicationToken) (ChangeSet, error) {
	if p == nil || p.stage == nil || p.stage.owner != e || p.stage.lifecycle != catalogValidated {
		return ChangeSet{}, fmt.Errorf("stuntgres: invalid catalog publication")
	}
	op.session.mu.Lock()
	scope := op.timeoutScope
	op.session.mu.Unlock()
	if scope == nil || scope.op != op || scope.owner != op.session.transactions || !scope.ownsMessageScope() || !scope.owner.retainsTransaction() {
		return ChangeSet{}, fmt.Errorf("stuntgres: missing message scope")
	}
	if token.Epoch != p.epoch {
		return ChangeSet{}, ErrCatalogReplaced
	}
	if err := e.stageCatalogOverlay(scope, p.stage, cloneCatalogChanges(scope.owner.catalog)); err != nil {
		return ChangeSet{}, err
	}
	return catalogStageChanges(p.stage), nil
}
