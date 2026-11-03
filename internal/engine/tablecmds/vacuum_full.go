package tablecmds

import (
	"fmt"
	"maps"
	"math"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqlstate"
)

// vacuumFullRelation owns the granted AX contribution. All recipe callbacks and
// heap copying run detached; one callback-free Engine.mu window swaps the entire
// incarnation, catalog locator set, index roots and maintenance facts.
func vacuumFullRelation(e *core.Engine, s *core.StatementScope, selected table.VacuumTarget, o table.VacuumOptions) (bool, error) {
	e.Mu.Lock()
	if err := vacuumDatabasePreflightLocked(e, s); err != nil {
		e.Mu.Unlock()
		return false, err
	}
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		e.Mu.Unlock()
		return false, err
	}
	if selected.Ref.Domain() != core.DomainOf(e) || selected.Ref.Epoch != auth.Catalog.Epoch() {
		e.Mu.Unlock()
		return false, ids.ErrCatalogReplaced
	}
	d, exists := auth.Catalog.Relation(selected.Ref.Address)
	if !exists {
		e.Mu.Unlock()
		if selected.Name != "" {
			vacuumNotice(s, table.VacuumMissingWarning(selected.Name))
		}
		return false, nil
	}
	work, warning, err := table.VacuumWork(s.Op.Context(), auth.View, auth, auth.Session.SessionToken, d, o.ProcessMain)
	analyze := err == nil && warning == (table.VacuumWarning{}) && (table.VacuumHeap(d) || d.Partition != nil) && !table.VacuumForeignTemp(auth.Catalog, auth.Session.SessionToken, d) && (d.RelationKind != catalog.MaterializedViewRelation || d.Populated)
	if err != nil || !work || d.RelationKind == catalog.MaterializedViewRelation && !d.Populated {
		e.Mu.Unlock()
		if warning != (table.VacuumWarning{}) {
			vacuumNotice(s, warning)
		}
		return analyze, err
	}
	store := e.Storage.Relations[d.Address()]
	if store == nil {
		e.Mu.Unlock()
		return false, ids.ErrCatalogReplaced
	}
	if s.Op.Session.SessionToken.ID() > math.MaxInt32 {
		e.Mu.Unlock()
		return false, fmt.Errorf("stuntgres: vacuum session identity exceeds integer range")
	}
	target := vacuumHeapTarget{selected.Ref, d, store}
	capture, allowed, err := captureVacuumLocked(e, s, target)
	if err != nil || !allowed {
		e.Mu.Unlock()
		return false, err
	}
	capture.CaptureIndexesLocked(&e.Catalog.Owner, e.Storage.IndexRoots, true)
	if e.ClusterProgress == nil {
		e.ClusterProgress = make(map[ids.TxRef]core.ClusterProgress)
	}
	e.ClusterProgress[s.TxRef] = core.ClusterProgress{Fact: pgcatalog.ClusterProgressFact{PID: int64(s.Op.Session.SessionToken.ID()), Database: table.VacuumDatabaseOID(auth.Catalog), DatabaseName: s.Op.Session.DatabaseName, Relation: d.Address().ObjectID, Phase: "initializing"}, Owner: auth.State.Authenticated}
	e.Mu.Unlock()
	defer func() { e.Mu.Lock(); delete(e.ClusterProgress, s.TxRef); e.Mu.Unlock() }()
	ctx := s.ActivateRuntime(s.Op.Context())
	progress := func(phase string, scanned, written, indexes int64) {
		e.Mu.Lock()
		if p, ok := e.ClusterProgress[s.TxRef]; ok && s.TxRef.Epoch() == e.Catalog.State.Epoch() {
			p.Fact.Phase, p.Fact.Scanned, p.Fact.Written, p.Fact.Indexes = phase, scanned, written, indexes
			e.ClusterProgress[s.TxRef] = p
		}
		e.Mu.Unlock()
	}
	for {
		e.Mu.Lock()
		maintenance := store.Maintenance
		metadata := e.Storage.RelationMetadata[d.Address()]
		origin, truncateBoundary := store.RetainedOrigin, store.TruncateBoundary
		materializedLineage, truncateLineage := store.MaterializedLineage, store.TruncateLineage
		e.Mu.Unlock()
		nextCatalog, err := prepareFullRewriteCatalog(e, s, capture)
		if err != nil {
			return false, err
		}
		var nextMetadata *storage.RelationMetadata
		if metadata != nil && metadata.Imported != nil {
			next := *metadata
			next.Imported = storage.WithoutImportedRelationStatistics(next.Imported, storage.RelationStatisticsAll)
			nextMetadata = &next
		}
		progress("seq scanning heap", 0, 0, 0)
		if err := ctx.Err(); err != nil {
			return false, err
		}
		access := s.WriteHost().CaptureRelationAccess(selected.Ref)
		access.Record(1, 0, 0)
		candidate, err := storage.RewriteHeapVacuum(ctx, storage.Relation{Versions: capture.Rows, Ends: capture.Ends, Retained: capture.Retained, Discarded: capture.Discarded, Headers: capture.Headers}, capture.Frontier, capture.Cutoff, capture.Retire, storage.VacuumProgress{
			Fetched: func() { access.Record(0, 1, 0) },
			Completed: func(scanned int, written int64) { progress("seq scanning heap", int64(scanned), written, 0) },
		})
		if err != nil {
			return false, err
		}
		nextStore, err := storage.NewRewriteRelation(candidate, capture.Generation, maintenance)
		if err != nil {
			return false, err
		}
		nextStore.RetainedOrigin = origin
		nextStore.TruncateBoundary = truncateBoundary
		nextStore.MaterializedLineage, nextStore.TruncateLineage = materializedLineage, truncateLineage
		if nextStore.RetainedOrigin == 0 {
			nextStore.RetainedOrigin = capture.Relation.EffectiveStorageLocator(catalog.NewRead(&e.Catalog.Owner, capture.Catalog))
		}
		if nextStore.RetainedOrigin == 0 {
			return false, fmt.Errorf("stuntgres: FULL rewrite has no predecessor storage locator")
		}
		roots := make(map[ids.ObjectAddress]*storage.IndexRoot, capture.Attached)
		var rebuilt int64
		err = s.WithMaintenanceOwner(capture.Relation, func(evaluation *exec.Context) error {
			e.Mu.Lock()
			ownerAuth, err := s.RoutineAuthorizationLocked()
			e.Mu.Unlock()
			if err != nil {
				return err
			}
			// The captured descriptor set is immutable and is revalidated in the
			// final preflight. Current operation authority owns callback execution.
			evaluation.Catalog = catalog.NewRead(&e.Catalog.Owner, capture.Catalog)
			for address, idx := range capture.Catalog.Relations() {
				if idx.SQLIndex == nil || idx.SQLIndex.Parent != d.Address() {
					continue
				}
				progress("rebuilding index", int64(len(capture.Rows)), int64(len(candidate.Versions)), rebuilt)
				if idx.SQLIndex.HasRuntimeDomainConversion() {
					evaluation.Input.TypeDiagnosticName, err = core.CaptureTypeDiagnosticName(ownerAuth)
					if err != nil {
						return err
					}
				}
				root, err := index.BuildRelationIndexRoot(evaluation, capture.Relation, idx, nextStore, nil)
				if err != nil {
					return err
				}
				namespace, ok := evaluation.Catalog.Namespace(d.Namespace())
				if !ok {
					return ids.ErrCatalogReplaced
				}
				if err := index.ValidateUniqueRoot(s.Op.Context(), capture.Relation, idx, root, namespace.Name(), evaluation.Output, &ownerAuth.View, core.DisclosureEnv(&ownerAuth), true); err != nil {
					return err
				}
				roots[address] = root
				rebuilt++
				progress("rebuilding index", int64(len(capture.Rows)), int64(len(candidate.Versions)), rebuilt)
			}
			return s.Op.Context().Err()
		})
		if err != nil {
			return false, err
		}
		if err := vacuumFullCompletion(s); err != nil {
			return false, err
		}
		e.Mu.Lock()
		current, allowed, err := captureVacuumLocked(e, s, target)
		if err != nil || !allowed {
			e.Mu.Unlock()
			if err == nil {
				vacuumNotice(s, table.VacuumPermissionWarning(d.Name()))
			}
			return false, err
		}
		current.CaptureIndexesLocked(&e.Catalog.Owner, e.Storage.IndexRoots, false)
		if !capture.SameLocked(current, &e.Storage, true) || e.Storage.RelationMetadata[d.Address()] != metadata || store.Maintenance != maintenance || store.RetainedOrigin != origin || store.TruncateBoundary != truncateBoundary || store.MaterializedLineage != materializedLineage || store.TruncateLineage != truncateLineage {
			current.CaptureIndexesLocked(&e.Catalog.Owner, e.Storage.IndexRoots, true)
			capture = current
			e.Mu.Unlock()
			continue
		}
		var expandedRoots map[ids.ObjectAddress]*storage.IndexRoot
		if current.Attached != current.IndexCount {
			expandedRoots = maps.Clone(e.Storage.IndexRoots)
			if expandedRoots == nil {
				expandedRoots = make(map[ids.ObjectAddress]*storage.IndexRoot, len(roots))
			}
			for address, root := range roots {
				expandedRoots[address] = root
			}
		}
		if err := s.Op.Context().Err(); err != nil {
			e.Mu.Unlock()
			return false, err
		}
		if p, ok := e.ClusterProgress[s.TxRef]; ok {
			p.Fact.Phase = "swapping relation files"
			e.ClusterProgress[s.TxRef] = p
		}
		e.Storage.InstallRelationLocked(d.Address(), nextStore)
		if expandedRoots != nil {
			e.Storage.ReplaceIndexRootsLocked(expandedRoots)
		} else {
			for address, root := range roots {
				e.Storage.InstallIndexRootLocked(address, root)
			}
		}
		if nextMetadata != nil {
			e.Storage.RelationMetadata[d.Address()] = nextMetadata
		}
		e.Catalog.PublishLocked(nextCatalog)
		if p, ok := e.ClusterProgress[s.TxRef]; ok {
			p.Fact.Phase = "performing final cleanup"
			e.ClusterProgress[s.TxRef] = p
		}
		e.Mu.Unlock()
		if o.Verbose {
			message := table.VacuumFullReport(catalog.NewRead(&e.Catalog.Owner, capture.Catalog), capture.Relation, candidate, rebuilt)
			s.Op.EmitNotice(session.LevelInfo, sqlstate.Error{Severity: "INFO", SeverityUnlocalized: "INFO", Code: "00000", Message: message})
		}
		return true, nil
	}
}

// The detached completion boundary checks cancellation before publication locks.
// The final locked preflight checks again immediately before the first install.
func vacuumFullCompletion(s *core.StatementScope) error {
	return s.Op.Context().Err()
}

// Catalog candidates and descriptor copies are built detached. Only reservation
// of new identities uses Engine.mu; allocator gaps on rejected work are harmless.
func prepareFullRewriteCatalog(e *core.Engine, s *core.StatementScope, c table.VacuumCapture) (*catalog.State, error) {
	type replacement struct {
		descriptor *catalog.Relation
		locator    ids.OID
	}
	replacements := make([]replacement, 0, c.Attached+1)
	for address, d := range c.Catalog.Relations() {
		if address == c.Relation.Address() || d.SQLIndex != nil && d.SQLIndex.Parent == c.Relation.Address() {
			replacements = append(replacements, replacement{descriptor: d})
		}
	}
	e.Mu.Lock()
	if err := vacuumDatabasePreflightLocked(e, s); err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	for i := range replacements {
		var err error
		replacements[i].locator, err = e.Catalog.AllocateOIDLocked()
		if err != nil {
			e.Mu.Unlock()
			return nil, err
		}
	}
	e.Mu.Unlock()
	next := catalog.CloneState(c.Catalog)
	for _, r := range replacements {
		d := *r.descriptor
		d.StorageLocator = r.locator
		if d.Address() == c.Relation.Address() && c.Attached == 0 {
			d.History.HasIndexes = false
		}
		next.SetDescriptor(d.Address(), &d)
	}
	return next, nil
}
