package engine

import (
	"fmt"
	"maps"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
)

// catalogIndexFence records the committed indexes of parent, so publication
// fails when an index of parent was created or dropped.
type catalogIndexFence struct {
	parent  ids.ObjectAddress
	indexes map[ids.ObjectAddress]bool
}

func newCatalogIndexFence(committed *catalog.State, parent ids.ObjectAddress) catalogIndexFence {
	f := catalogIndexFence{parent: parent, indexes: map[ids.ObjectAddress]bool{}}
	for a, d := range committed.Descriptors() {
		r, ok := d.(*catalog.Relation)
		if ok && r.SQLIndex != nil && r.SQLIndex.Parent == parent {
			f.indexes[a] = true
		}
	}
	return f
}

func (catalogIndexFence) Kind() session.FenceKind { return fenceIndexSet }

func (f catalogIndexFence) current(committed *catalog.State, _ map[ids.ObjectAddress]*storage.Relation) error {
	count := 0
	for a, d := range committed.Descriptors() {
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

// catalogNamespaceFence records the committed members of a removed namespace,
// so publication fails when a member changed or was added.
type catalogNamespaceFence struct {
	namespace ids.ObjectAddress
	members   map[ids.ObjectAddress]catalog.Descriptor
}

func newCatalogNamespaceFence(committed *catalog.State, namespace ids.ObjectAddress) catalogNamespaceFence {
	f := catalogNamespaceFence{namespace: namespace, members: map[ids.ObjectAddress]catalog.Descriptor{}}
	for a, d := range committed.Descriptors() {
		if d.Namespace() == namespace {
			f.members[a] = d
		}
	}
	return f
}

func (catalogNamespaceFence) Kind() session.FenceKind { return fenceNamespace }

func (f catalogNamespaceFence) current(committed *catalog.State, _ map[ids.ObjectAddress]*storage.Relation) error {
	count := 0
	for a, d := range committed.Descriptors() {
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
	for _, fence := range typedef.DomainJournalOf(c.Domain).ValidationFences() {
		if !fence.Current(e.catalog.read(e.catalog.state), e.storage.Relations) {
			return catalog.Conflict{}
		}
	}
	for address, root := range typedef.DomainJournalOf(c.Domain).CommittedIndexRoots() {
		if e.storage.IndexRoots[address] != root {
			return catalog.Conflict{}
		}
	}
	return e.catalogFencesCurrentLocked(c, fenceStorageRead, fenceIndexSet, fenceNamespace)
}

// Capture committed predecessor fences while checking the exact private roots
// used by the ordinary stage. Pending writes are not committed observations.
func (e *Engine) captureCatalogStageLocked(s *CatalogStage, c *session.Journal, current *catalog.State, authority *catalogAuthority) error {
	if err := s.DefaultPreflight(current); err != nil {
		return err
	}
	if err := e.captureDomainStageLocked(s, c, current); err != nil {
		return err
	}
	if r := s.materializedOrZero().Replacement; r != nil {
		if err := s.materializedReplacementCurrent(current, e.stageStorageLocked(s, r.Original.Address())); err != nil {
			return err
		}
	}
	capture := journalCapture{journal: c, candidate: &s.Candidate, committed: e.catalog.state, current: current}
	if s.databasePredecessor != nil {
		if err := capture.observeDatabase(e.catalog.owner().DatabaseAddress(), s.databaseAuthority, s.databasePredecessor); err != nil {
			return err
		}
		if authority != nil {
			authority.databaseAuthority = s.databaseAuthority
			authority.databasePredecessor = s.databasePredecessor
		}
	}
	if err := capture.observeCandidate(); err != nil {
		return err
	}
	domainBases := s.domainBases
	if authority != nil && authority.session == s.authSession && authority.authorization == s.authRevision && authority.roles == s.roleRevision &&
		len(domainBases) != 0 && len(authority.observations) == len(domainBases) && &authority.observations[0] == &domainBases[0] {
		// These exact validated providers belong to this statement's Finish
		// authority, not to the outer transaction's predecessor fences.
		domainBases = nil
	}
	for _, source := range s.querySources {
		if !view.MaterializedSourceCurrent(s.materializedOrZero().SourceView, source, current, e.stageStorageLocked(s, source.Address())) {
			return catalog.Conflict{}
		}
		if snapshot := s.materializedOrZero().SourceView; snapshot != nil {
			if relation, ok := source.(*catalog.Relation); ok && relation.RelationKind == catalog.MaterializedViewRelation {
				fence := materializedSourceFence{ref: e.catalog.Domain.Ref(snapshot.Catalog.Epoch(), relation.Address()), relation: relation, storage: snapshot.GenerationAt(relation.Address())}
				if fence.storage != nil && e.materializedSourceFenceCurrent(fence) && c.Descriptors[relation.Address()] == nil {
					c.AddFence(fence)
					continue
				}
			}
		}
		if err := capture.observeBase(source.Address()); err != nil {
			return err
		}
	}
	for _, sources := range [][]catalog.Descriptor{s.tableOrZero().LikeSources, domainBases} {
		for _, d := range sources {
			if err := capture.observeBase(d.Address()); err != nil {
				return err
			}
		}
	}
	stageIndexes := s.indexesOrZero()
	for a, d := range stageIndexes.Parents {
		if err := capture.observe(a, d); err != nil {
			return err
		}
	}
	for a, d := range stageIndexes.Rebuilds {
		if err := capture.observe(a, d); err != nil {
			return err
		}
		if index.JournalIndexesOrZero(c).Rebuilds == nil {
			index.JournalIndexes(c).Rebuilds = map[ids.ObjectAddress]*catalog.Relation{}
		}
		index.JournalIndexes(c).Rebuilds[a] = d
	}
	if err := capture.fenceNames(maps.Keys(stageIndexes.Sets)); err != nil {
		return err
	}
	captureStorage := func(a ids.ObjectAddress) {
		c.AddFence(newStorageReadFence(a, e.storage.Relations[a]))
	}
	for _, f := range s.storageReads {
		captureStorage(f.address)
	}
	for _, d := range stageIndexes.Parents {
		captureStorage(d.Address())
	}
	for a, candidate := range stageIndexes.Candidates {
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
		if !candidate.MatchesIndex(index, store) {
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
