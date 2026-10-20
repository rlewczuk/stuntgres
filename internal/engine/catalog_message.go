package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

type catalogIndexFence struct {
	parent  ids.ObjectAddress
	indexes map[ids.ObjectAddress]bool
}

func (catalogIndexFence) Kind() session.FenceKind { return fenceIndexSet }

func (f catalogIndexFence) currentLocked(e *Engine, _ *session.Journal) error {
	count := 0
	for a, d := range e.catalog.state.Descriptors() {
		r, ok := d.(*catalog.Relation)
		if ok && r.SQLIndex != nil && r.SQLIndex.Parent == f.parent {
			if !f.indexes[a] {
				return catalog.Conflict{}
			}
			count++
		}
	}
	if count != len(f.indexes) {
		return catalog.Conflict{}
	}
	return nil
}

type catalogNamespaceFence struct {
	namespace ids.ObjectAddress
	members   map[ids.ObjectAddress]catalog.Descriptor
}

func (catalogNamespaceFence) Kind() session.FenceKind { return fenceNamespace }

func (f catalogNamespaceFence) currentLocked(e *Engine, _ *session.Journal) error {
	count := 0
	for a, d := range e.catalog.state.Descriptors() {
		if d.Namespace() == f.namespace {
			if f.members[a] != d {
				return catalog.Conflict{}
			}
			count++
		}
	}
	if count != len(f.members) {
		return catalog.Conflict{}
	}
	return nil
}

func (e *Engine) stageCatalogBaseLocked(s *CatalogStage) *catalog.State {
	if s.authSession != nil && s.authSession.Transactions.RetainsTransaction() {
		return e.privateCatalogLocked(s.authSession.Transactions.Catalog).State()
	}
	return e.catalog.state
}
func (e *Engine) stageStorageLocked(s *CatalogStage, a ids.ObjectAddress) *storage.Relation {
	if s.authSession != nil && s.authSession.Transactions.RetainsTransaction() {
		return e.privateStorageLocked(s.authSession.Transactions.Catalog, a)
	}
	return e.storage.Relations[a]
}
func (e *Engine) catalogReadPreflightLocked(c *session.Journal) error {
	for _, fence := range domainJournalOf(c.Domain).ValidationFences() {
		if !fence.Current(e.catalog.read(e.catalog.state), e.storage.Relations) {
			return catalog.Conflict{}
		}
	}
	for address, root := range domainJournalOf(c.Domain).CommittedIndexRoots() {
		if e.storage.IndexRoots[address] != root {
			return catalog.Conflict{}
		}
	}
	return e.catalogFencesCurrentLocked(c, fenceStorageRead, fenceIndexSet, fenceNamespace)
}

// Capture committed predecessor fences while checking the exact private roots
// used by the ordinary stage. Pending writes are not committed observations.
func (e *Engine) captureCatalogStageLocked(s *CatalogStage, c *session.Journal, current *catalog.State, authority *catalogAuthority) error {
	if err := s.defaultPreflight(current); err != nil {
		return err
	}
	if err := e.captureDomainStageLocked(s, c, current); err != nil {
		return err
	}
	if r := s.materializedOrZero().replacement; r != nil {
		if err := s.materializedReplacementCurrent(current, e.stageStorageLocked(s, r.original.Address())); err != nil {
			return err
		}
	}
	observe := func(a ids.ObjectAddress, d catalog.Descriptor) error {
		if current.Get(a) != d {
			return catalog.Conflict{}
		}
		if _, ok := c.Originals[a]; !ok {
			c.Originals[a] = e.catalog.state.Get(a)
		}
		return nil
	}
	if s.databasePredecessor != nil {
		if current.Get(catalog.CurrentDatabaseAddress) != s.databaseAuthority || e.catalog.state.Get(catalog.CurrentDatabaseAddress) != s.databasePredecessor {
			return ids.ErrCatalogReplaced
		}
		if original, ok := c.Originals[catalog.CurrentDatabaseAddress]; ok && original != s.databasePredecessor {
			return catalog.Conflict{}
		}
		c.Originals[catalog.CurrentDatabaseAddress] = s.databasePredecessor
		if authority != nil {
			authority.databaseAuthority = s.databaseAuthority
			authority.databasePredecessor = s.databasePredecessor
		}
	}
	for a := range s.Descriptors() {
		if err := observe(a, s.Base.Get(a)); err != nil {
			return err
		}
	}
	for a := range s.Removals() {
		if err := observe(a, s.Base.Get(a)); err != nil {
			return err
		}
	}
	observeNamespace := func(ns ids.ObjectAddress) error {
		if d := s.Base.Get(ns); d != nil {
			// TEMP creation can add its namespace to the candidate before
			// constructing the ordinary stage. That exact creation intent is
			// part of the stage's entry authority, not a later projection.
			// Its committed absence/name fences already belong to c.
			if current.Get(ns) == nil {
				original, created := c.Originals[ns]
				key := ids.NameKey{ClassID: ids.NamespaceClassID, Name: d.Name()}
				_, named := c.Names[key]
				if created && original == nil && c.Descriptors[ns] == d && named && current.NameAddress(key) == (ids.ObjectAddress{}) {
					return nil
				}
			}
			return observe(ns, d)
		}
		return nil
	}
	for _, d := range s.Descriptors() {
		if err := observeNamespace(d.Namespace()); err != nil {
			return err
		}
		if old := s.Base.Get(d.Address()); old != nil {
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
		if !materializedSourceCurrent(s.materializedOrZero().sourceView, source, current, e.stageStorageLocked(s, source.Address())) {
			return catalog.Conflict{}
		}
		if snapshot := s.materializedOrZero().sourceView; snapshot != nil {
			if relation, ok := source.(*catalog.Relation); ok && relation.RelationKind == catalog.MaterializedViewRelation {
				fence := materializedSourceFence{ref: e.catalog.Domain.Ref(snapshot.Catalog.Epoch(), relation.Address()), relation: relation, storage: snapshot.GenerationAt(relation.Address())}
				if fence.storage != nil && e.materializedSourceFenceCurrent(fence) && c.Descriptors[relation.Address()] == nil {
					c.AddFence(fence)
					continue
				}
			}
		}
		if observed := s.Base.Get(source.Address()); observed != nil {
			if err := observe(source.Address(), observed); err != nil {
				return err
			}
		}
	}
	for _, sources := range [][]catalog.Descriptor{s.likeSources, domainBases} {
		for _, d := range sources {
			observed := s.Base.Get(d.Address())
			if observed != nil {
				if err := observe(d.Address(), observed); err != nil {
					return err
				}
			}
		}
	}
	stageIndexes := s.indexesOrZero()
	for a, d := range stageIndexes.parents {
		if err := observe(a, d); err != nil {
			return err
		}
	}
	for a, d := range stageIndexes.rebuilds {
		if err := observe(a, d); err != nil {
			return err
		}
		if journalIndexesOrZero(c).rebuilds == nil {
			journalIndexes(c).rebuilds = map[ids.ObjectAddress]*catalog.Relation{}
		}
		journalIndexes(c).rebuilds[a] = d
	}
	for k, a := range s.Touched() {
		if current.NameAddress(k) != a {
			return catalog.Conflict{}
		}
		if _, ok := c.Names[k]; !ok {
			c.Names[k] = e.catalog.state.NameAddress(k)
		}
	}
	for parent := range stageIndexes.sets {
		for address, descriptor := range current.Descriptors() {
			index, ok := descriptor.(*catalog.Relation)
			if ok && index.SQLIndex != nil && index.SQLIndex.Parent == parent {
				if err := observe(address, index); err != nil {
					return err
				}
			}
		}
		f := catalogIndexFence{parent: parent, indexes: map[ids.ObjectAddress]bool{}}
		for a, d := range e.catalog.state.Descriptors() {
			r, ok := d.(*catalog.Relation)
			if ok && r.SQLIndex != nil && r.SQLIndex.Parent == parent {
				f.indexes[a] = true
			}
		}
		c.AddFence(f)
	}
	for a := range s.Removals() {
		if a.ClassID == ids.NamespaceClassID {
			f := catalogNamespaceFence{namespace: a, members: map[ids.ObjectAddress]catalog.Descriptor{}}
			for address, d := range e.catalog.state.Descriptors() {
				if d.Namespace() == a {
					f.members[address] = d
				}
			}
			c.AddFence(f)
		}
	}
	captureStorage := func(a ids.ObjectAddress) {
		store := e.storage.Relations[a]
		f := partitionReadFence{address: a, storage: store}
		if store != nil {
			f.length = len(store.Versions)
			f.ends = store.Ends
			f.generation = store.Generation
		}
		c.AddFence(f)
	}
	for _, f := range s.partitionReads {
		captureStorage(f.address)
	}
	for _, d := range stageIndexes.parents {
		captureStorage(d.Address())
	}
	for a, candidate := range stageIndexes.candidates {
		index, ok := s.Get(a).(*catalog.Relation)
		if !ok {
			index, ok = current.Relation(a)
		}
		if !ok || index.SQLIndex == nil {
			return catalog.Conflict{}
		}
		// Candidate data belongs to the actual overlay target, not the
		// committed/private predecessor used by captureStorage's read fence.
		shape, staged := c.Storage[index.SQLIndex.Parent]
		store := shape.Replacement
		if !staged {
			store = e.stageStorageLocked(s, index.SQLIndex.Parent)
		}
		if shape, changed := s.rowShapes[index.SQLIndex.Parent]; changed {
			// capture precedes stageCatalogChangesLocked: supplied generation
			// roots belong to this stage's destination heap, not c's predecessor.
			store = shape.replacement
		}
		if !candidate.matchesIndex(index, store) {
			return catalog.Conflict{}
		}
		captureStorage(index.SQLIndex.Parent)
	}
	c.Dependencies = append(c.Dependencies, s.sequence.IdentityDependencyList()...)
	if s.authSession != nil && c.RolesBase == nil {
		c.RolesBase = e.roles.published
	}
	c.DomainChanged = c.DomainChanged || len(s.domainTargets) != 0
	for _, d := range s.Descriptors() {
		if typ, ok := d.(*catalog.Type); ok && typ.SQLType.IsUserDefined() {
			c.DomainChanged = true
			break
		}
	}
	return nil
}

func (e *Engine) stageMessageCatalog(op *session.Operation, p *PreparedCatalogPublication, token PublicationToken) (ChangeSet, error) {
	if p == nil || p.stage == nil || p.stage.owner != e || p.stage.lifecycle != catalogValidated {
		return ChangeSet{}, fmt.Errorf("stuntgres: invalid catalog publication")
	}
	op.Session.Mu.Lock()
	scope := scopeOfLease(op.Lease)
	op.Session.Mu.Unlock()
	if scope == nil || scope.op != op || scope.owner != op.Session.Transactions || !scope.ownsMessageScope() || !scope.owner.RetainsTransaction() {
		return ChangeSet{}, fmt.Errorf("stuntgres: missing message scope")
	}
	if token.Epoch != p.epoch {
		return ChangeSet{}, ids.ErrCatalogReplaced
	}
	if err := e.stageCatalogOverlay(scope, p.stage, scope.owner.Catalog.Clone()); err != nil {
		return ChangeSet{}, err
	}
	return catalogStageChanges(p.stage), nil
}
