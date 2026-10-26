package core

import (
	"fmt"
	"maps"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
)

func stageJournalLocked(s *CatalogStage) *session.Journal {
	if s.statement != nil && !s.statement.Finished && s.statement.Catalog != nil {
		return s.statement.Catalog
	}
	if s.AuthSession != nil && (s.AuthSession.Transactions.State == session.TxActive || s.AuthSession.Transactions.RetainsTransaction()) {
		return s.AuthSession.Transactions.Catalog
	}
	return nil
}
func (e *Engine) StageCatalogBaseLocked(s *CatalogStage) *catalog.State {
	return e.PrivateCatalogLocked(stageJournalLocked(s)).State()
}
func (e *Engine) StageStorageLocked(s *CatalogStage, a ids.ObjectAddress) *storage.Relation {
	return e.PrivateStorageLocked(stageJournalLocked(s), a)
}
func (e *Engine) storageReadsValidLocked(stage *CatalogStage) bool {
	for _, read := range stage.StorageReads {
		if current := e.StageStorageLocked(stage, read.Address()); current == nil || !read.Matches(current) {
			return false
		}
	}
	return true
}
func (e *Engine) catalogReadPreflightLocked(c *session.Journal) error {
	for _, fence := range typedef.DomainJournalOf(c.Domain).ValidationFences() {
		if !fence.Current(e.Catalog.Current(), e.Storage.Relations) {
			return catalog.Conflict{}
		}
	}
	for address, root := range typedef.DomainJournalOf(c.Domain).CommittedIndexRoots() {
		if e.Storage.IndexRoots[address] != root {
			return catalog.Conflict{}
		}
	}
	return e.catalogFencesCurrentLocked(c, session.FenceStorageRead, session.FenceIndexSet, session.FenceNamespace)
}

// catalogRoleReadFence retains a pending producer's committed role predecessor
// until its rows/catalog publish. It is not a private role write: later own role
// changes use RolesBase independently, and completed metadata-only statements
// retain neither this read fence nor their execution authorization.
type catalogRoleReadFence struct {
	roles *access.RoleRecord
}

func (catalogRoleReadFence) Kind() session.FenceKind { return fenceCatalogRoleRead }

func (f catalogRoleReadFence) CurrentLocked(e *Engine, _ *session.Journal) error {
	if e.RoleStore.Published != f.roles {
		return catalog.Conflict{}
	}
	return nil
}

// Capture committed predecessor fences while checking the exact private roots
// used by the ordinary stage. Pending writes are not committed observations.
func (e *Engine) captureCatalogStageLocked(s *CatalogStage, c *session.Journal, current *catalog.State, authority *session.CatalogAuthority) error {
	// Cleanup candidates derive from the operation's exact private predecessor,
	// while the journal keeps the original committed root as its commit fence.
	if err := s.validateRoleCandidate(e.sessionRolesLocked(s.AuthSession)); err != nil {
		return err
	}
	if err := s.DefaultPreflight(current); err != nil {
		return err
	}
	if err := e.captureDomainStageLocked(s, c, current); err != nil {
		return err
	}
	if r := s.MaterializedOrZero().Replacement; r != nil {
		if err := s.materializedReplacementCurrent(current, e.StageStorageLocked(s, r.Original.Address())); err != nil {
			return err
		}
	}
	capture := session.JournalCapture{Journal: c, Candidate: &s.Candidate, Committed: e.Catalog.State, Current: current}
	if s.DatabasePredecessor != nil {
		if err := capture.ObserveDatabase(e.Catalog.Owner.DatabaseAddress(), s.DatabaseAuthority, s.DatabasePredecessor); err != nil {
			return err
		}
		if authority != nil {
			authority.DatabaseAuthority = s.DatabaseAuthority
			authority.DatabasePredecessor = s.DatabasePredecessor
		}
	}
	if err := capture.ObserveCandidate(); err != nil {
		return err
	}
	for _, d := range s.Descriptors() {
		if err := visitDefinitionProviders(s.Base, d, capture.ObserveBase); err != nil {
			return err
		}
	}
	domainBases := s.DomainBases
	if authority != nil && authority.Session == s.AuthSession && authority.Authorization == s.AuthRevision && authority.Roles == s.RoleRevision &&
		len(domainBases) != 0 && len(authority.Observations) == len(domainBases) && &authority.Observations[0] == &domainBases[0] {
		// These exact validated providers belong to this statement's Finish
		// authority, not to the outer transaction's predecessor fences.
		domainBases = nil
	}
	for _, source := range s.QuerySources {
		if !view.MaterializedSourceCurrent(s.MaterializedOrZero().SourceView, source, current, e.StageStorageLocked(s, source.Address())) {
			return catalog.Conflict{}
		}
		if snapshot := s.MaterializedOrZero().SourceView; snapshot != nil {
			if relation, ok := source.(*catalog.Relation); ok && relation.RelationKind == catalog.MaterializedViewRelation {
				fence := materializedSourceFence{ref: e.Catalog.Domain.Ref(snapshot.Catalog.Epoch(), relation.Address()), relation: relation, storage: snapshot.GenerationAt(relation.Address())}
				if fence.storage != nil && e.materializedSourceFenceCurrent(fence) && c.Descriptors[relation.Address()] == nil {
					c.AddFence(fence)
					continue
				}
			}
		}
		if err := capture.ObserveBase(source.Address()); err != nil {
			return err
		}
	}
	for _, sources := range [][]catalog.Descriptor{s.tableOrZero().LikeSources, domainBases, s.definitionSources} {
		for _, d := range sources {
			if err := capture.ObserveBase(d.Address()); err != nil {
				return err
			}
		}
	}
	stageIndexes := s.IndexesOrZero()
	for a, d := range stageIndexes.Parents {
		if err := capture.Observe(a, d); err != nil {
			return err
		}
	}
	for a, d := range stageIndexes.Rebuilds {
		if err := capture.Observe(a, d); err != nil {
			return err
		}
		if index.JournalIndexesOrZero(c).Rebuilds == nil {
			index.JournalIndexes(c).Rebuilds = map[ids.ObjectAddress]*catalog.Relation{}
		}
		index.JournalIndexes(c).Rebuilds[a] = d
	}
	if err := capture.FenceNames(maps.Keys(stageIndexes.Sets)); err != nil {
		return err
	}
	captureStorage := func(a ids.ObjectAddress) {
		c.AddFence(session.NewStorageReadFence(a, e.Storage.Relations[a]))
	}
	for _, f := range s.StorageReads {
		captureStorage(f.Address())
	}
	for _, d := range stageIndexes.Parents {
		captureStorage(d.Address())
	}
	// A mismatch discards c, so fencing each parent before its match keeps
	// the successful fence order.
	if !index.CandidatesMatch(stageIndexes.Candidates, &s.Candidate, current, func(parent ids.ObjectAddress) *storage.Relation {
		captureStorage(parent)
		if shape, changed := s.RowShapes[parent]; changed {
			// capture precedes stageCatalogChangesLocked: supplied generation
			// roots belong to this stage's destination heap, not c's predecessor.
			return shape.Replacement
		}
		// Candidate data belongs to the actual overlay target, not the
		// committed/private predecessor used by captureStorage's read fence.
		if shape, staged := c.Storage[parent]; staged {
			return shape.Replacement
		}
		return e.StageStorageLocked(s, parent)
	}) {
		return catalog.Conflict{}
	}
	c.Dependencies = append(c.Dependencies, s.Sequence.IdentityDependencyList()...)
	// Detached producer rows are still pending publication after statement
	// Finish. Keep their role read separate from actual private role writes.
	if s.InitialTx != (ids.TxRef{}) || s.MaterializedOrZero().Replacement != nil {
		c.AddFence(catalogRoleReadFence{roles: e.RoleStore.Published})
	}
	if s.RoleCandidateRecord != nil && c.RolesBase == nil {
		c.RolesBase = e.RoleStore.Published
	}
	c.DomainChanged = c.DomainChanged || len(s.DomainTargets) != 0
	for _, d := range s.Descriptors() {
		if typ, ok := d.(*catalog.Type); ok && typ.SQLType.IsUserDefined() {
			c.DomainChanged = true
			break
		}
	}
	return nil
}

func (e *Engine) stageMessageCatalog(op *session.Operation, p *PreparedCatalogPublication, token PublicationToken) (ChangeSet, error) {
	if p == nil || p.stage == nil || p.stage.Owner != e || p.stage.Lifecycle != catalogValidated {
		return ChangeSet{}, fmt.Errorf("stuntgres: invalid catalog publication")
	}
	op.Session.Mu.Lock()
	scope := ActiveScopeOfLease(op.Lease)
	op.Session.Mu.Unlock()
	if scope == nil || scope.Op != op || scope.Owner != op.Session.Transactions || !scope.ownsMessageScope() || !scope.Owner.RetainsTransaction() {
		return ChangeSet{}, fmt.Errorf("stuntgres: missing message scope")
	}
	if token.Epoch != p.epoch {
		return ChangeSet{}, ids.ErrCatalogReplaced
	}
	if err := e.StageCatalogOverlay(scope, p.stage, scope.PendingCatalog().Clone()); err != nil {
		return ChangeSet{}, err
	}
	return catalogStageChanges(p.stage), nil
}
