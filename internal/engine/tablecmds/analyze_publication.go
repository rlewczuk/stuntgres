package tablecmds

import (
	"fmt"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqlstate"
)

// A target owns its captures and typed deltas. Only completed local maintenance
// crosses the first publication boundary; typed facts cross the final one.
type analyzeRun struct {
	e                *core.Engine
	s                *core.StatementScope
	target           analyzeTarget
	auth             core.AuthorizationView
	relation         *catalog.Relation
	closure          []*catalog.Relation
	columns          []storage.StatisticsColumn
	definitions      []*catalog.Statistics
	extended         []capturedExtendedInput
	extendedWarnings []sqlstate.Error
	segments         []*analyzeSegment
	delta            map[storage.StatisticsKey]*storage.ColumnStatistics
	extendedDelta    map[storage.ExtendedStatisticsKey]*storage.ExtendedStatistics
	hierarchy        *storage.StatisticsHierarchy
	started          time.Time
}

func analyzeRelation(e *core.Engine, s *core.StatementScope, target analyzeTarget, options table.AnalyzeOptions) error {
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	relation, ok := auth.Catalog.Relation(target.ref.Address)
	if !ok || target.ref.Epoch != auth.Catalog.Epoch() {
		e.Mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	allowed, err := table.VacuumAllowed(s.Op.Context(), auth.View, auth, relation)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	if !allowed {
		e.Mu.Unlock()
		analyzeWarning(s, "01000", fmt.Sprintf(`permission denied to analyze "%s", skipping it`, relation.Name()))
		return nil
	}
	if table.VacuumForeignTemp(auth.Catalog, s.Op.Session.SessionToken, relation) {
		e.Mu.Unlock()
		return nil
	}
	if relation.Foreign == nil && !table.VacuumHeap(relation) && relation.Partition == nil {
		e.Mu.Unlock()
		analyzeWarning(s, "01000", fmt.Sprintf(`skipping "%s" --- cannot analyze non-tables or special system tables`, relation.Name()))
		return nil
	}
	closure, err := analyzeClosure(auth.Catalog, relation, s.Op.Session.SessionToken)
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	r := &analyzeRun{e: e, s: s, target: target, auth: auth, relation: relation, closure: closure, started: time.Now(), extendedDelta: make(map[storage.ExtendedStatisticsKey]*storage.ExtendedStatistics)}
	defer r.close()
	var foreign *analyzeForeignSource
	if relation.Foreign != nil {
		foreign, err = captureAnalyzeForeign(e, s, auth, relation)
		if err != nil {
			return err
		}
		if foreign == nil {
			analyzeWarning(s, "01000", fmt.Sprintf(`skipping "%s" --- cannot analyze this foreign table`, relation.Name()))
			return nil
		}
	}
	r.columns, err = analyzeColumns(s, relation, target)
	if err != nil {
		return err
	}
	inherited := len(closure) > 1 || relation.Partition != nil
	if inherited {
		r.hierarchy = &storage.StatisticsHierarchy{Relations: closure, Session: s.Op.Session.SessionToken}
	}
	if len(r.columns) != 0 {
		r.definitions = analyzeExtendedDefinitions(auth.Catalog, relation)
		r.extended, r.extendedWarnings, err = captureExtendedStatistics(s, auth.Catalog, relation, r.columns, r.definitions)
		if err != nil {
			return err
		}
	}
	if relation.Partition == nil {
		var local *storage.RelationStatistics
		var rows uint64
		var foreignFact *storage.ForeignAnalyzeStatistics
		if foreign != nil {
			var pass *analyzeForeignPass
			pass, err = r.collectForeign(foreign, options)
			if err == nil {
				// Scalar Finish has completed. Definition evaluation/build and
				// accepted deltas are real, separate work before publication.
				local, foreignFact = pass.statistics, pass.foreign
				if foreignFact.SampleRows != 0 && foreignFact.TotalRows > 0 {
					for _, warning := range r.extendedWarnings {
						s.Op.EmitNotice(session.LevelWarning, warning)
					}
					for i := range r.extended {
						fact, buildErr := pass.buildExtended(&r.extended[i])
						if buildErr != nil {
							return buildErr
						}
						if err := r.acceptExtended(r.extended[i].Key, fact); err != nil {
							return err
						}
					}
				}
			}
		} else {
			err = r.captureNative([]*catalog.Relation{relation})
			if err == nil {
				if options.Verbose {
					r.notice()
				}
				local, rows, err = r.collect(false, r.segments, nil)
			}
		}
		if err != nil {
			return err
		}
		if local != nil {
			r.delta = local.Columns
		}
		// A leaf has one final boundary. A root completes local maintenance
		// first, retaining both typed flavors privately until inherited success.
		if err := r.publishLocal(rows, foreignFact, !inherited); err != nil {
			return err
		}
		if !inherited {
			return nil
		}
	} else if options.Verbose {
		r.notice()
	}
	var population float64
	if inherited {
		// Child AccessShare waits are never SKIP_LOCKED: that option controls
		// target admission, not inherited sampling.
		for _, child := range closure {
			if child.Address() != relation.Address() {
				if err := vacuumAcquire(s, auth.Ref(child.Address()), lock.AccessShare, false); err != nil {
					return err
				}
			}
		}
		visits, mixed, err := r.inheritedSegments()
		if err != nil {
			return err
		}
		var sampled *float64
		if mixed {
			population, err = r.allocate(visits)
			if err != nil {
				return err
			}
			sampled = &population
		}
		facts, count, err := r.collect(true, visits, sampled)
		if err != nil {
			return err
		}
		if !mixed {
			population = float64(count)
		}
		if facts != nil {
			if r.delta == nil {
				r.delta = facts.Columns
			} else {
				for key, fact := range facts.Columns {
					r.delta[key] = fact
				}
			}
		}
	}
	return r.publishFinal(population)
}

func (r *analyzeRun) notice() {
	qualified := r.relation.Name()
	if ns, ok := r.auth.Catalog.Namespace(r.relation.Namespace()); ok {
		qualified = ns.Name() + "." + qualified
	}
	r.s.Op.EmitNotice(session.LevelInfo, sqlstate.Error{Severity: "INFO", SeverityUnlocalized: "INFO", Code: "00000", Message: fmt.Sprintf(`analyzing "%s"`, qualified)})
}

// Called only in short metadata windows; all callbacks and waits are detached.
func (r *analyzeRun) authorityLocked() error {
	current, err := r.e.AuthorizationLocked(r.s.Op)
	if err != nil {
		return err
	}
	if current.Catalog.State() != r.auth.Catalog.State() || current.Roles != r.auth.Roles {
		return ids.ErrCatalogReplaced
	}
	now, ok := current.Catalog.Relation(r.relation.Address())
	if !ok || !index.SameRelationRowShape(now, r.relation) {
		return ids.ErrCatalogReplaced
	}
	for _, segment := range r.segments {
		if segment.store != nil && r.e.PrivateStorageLocked(r.s.PendingCatalog(), segment.relation.Address()) != segment.store {
			return ids.ErrCatalogReplaced
		}
	}
	allowed, err := table.VacuumAllowed(r.s.Op.Context(), current.View, current, now)
	if err != nil {
		return err
	}
	if !allowed {
		return core.SQLError("42501", fmt.Sprintf(`permission denied for table %s`, now.Name()))
	}
	return r.s.Op.Context().Err()
}

func (r *analyzeRun) metadataLocked() (*storage.RelationMetadata, *catalog.Relation) {
	original, _ := r.e.Catalog.State.Relation(r.relation.Address())
	metadata := &storage.RelationMetadata{}
	if previous := session.ResolveRelationMetadata(r.s.PendingCatalog(), r.e.Storage.RelationMetadata, r.relation.Address()); previous != nil {
		*metadata = *previous
	}
	return metadata, original
}

func (r *analyzeRun) journalLocked() *session.Journal {
	if r.s.Catalog == nil {
		pending := r.s.PendingCatalog()
		r.s.Catalog = pending.Clone()
		// Statistics-only writes do not change catalog authority. Keep the exact
		// cached private read, rather than manufacturing a new descriptor root.
		if pending != nil {
			r.s.Catalog.CachedBase, r.s.Catalog.Cached = pending.CachedBase, pending.Cached
		}
	}
	return r.s.Catalog
}

func (r *analyzeRun) installLocked(metadata *storage.RelationMetadata, original *catalog.Relation, typed bool) {
	s, e, relation := r.s, r.e, r.relation
	if s.Owner.InBlock() || s.PendingCatalog() != nil {
		r.journalLocked()
		var private *storage.RelationMetadata
		if original == nil {
			private = metadata
		}
		if typed {
			s.Catalog.RecordStatistics(relation.Address(), e.Storage.Relations[relation.Address()], original, relation, r.delta, r.extendedDelta, private)
		} else if private != nil {
			s.Catalog.RecordStatistics(relation.Address(), e.Storage.Relations[relation.Address()], original, relation, nil, nil, private)
		}
	} else if typed {
		metadata.Statistics = storage.MergeStatistics(metadata.Statistics, r.delta)
		metadata.Statistics = storage.MergeExtendedStatistics(metadata.Statistics, r.extendedDelta)
	}
	if original != nil {
		if e.Storage.RelationMetadata == nil {
			e.Storage.RelationMetadata = make(map[ids.ObjectAddress]*storage.RelationMetadata)
		}
		e.Storage.RelationMetadata[relation.Address()] = metadata
	}
}

func (r *analyzeRun) forgetImportedLocked(metadata *storage.RelationMetadata, mask uint8) {
	metadata.Imported = storage.WithoutImportedRelationStatistics(metadata.Imported, mask)
	if r.s.PendingCatalog() != nil {
		r.journalLocked().ForgetRelationStatistics(r.relation.Address(), mask)
	}
}

func (r *analyzeRun) publishLocal(rows uint64, foreign *storage.ForeignAnalyzeStatistics, final bool) error {
	r.e.Mu.Lock()
	defer r.e.Mu.Unlock()
	if err := r.authorityLocked(); err != nil {
		return err
	}
	metadata, original := r.metadataLocked()
	mask := uint8(storage.RelationTuples)
	if foreign != nil {
		mask |= storage.RelationPages
		metadata.ForeignAnalyze = foreign
	}
	r.forgetImportedLocked(metadata, mask)
	completed := time.Now().UTC().Truncate(time.Microsecond)
	metadata.AnalyzeCount++
	metadata.LastAnalyze = completed
	if foreign == nil {
		store := r.e.PrivateStorageLocked(r.s.PendingCatalog(), r.relation.Address())
		if store == nil {
			return ids.ErrCatalogReplaced
		}
		maintenance := r.e.Storage.Relations[r.relation.Address()]
		if maintenance == nil {
			maintenance = store
		}
		maintenance.Maintenance.Scanned, maintenance.Maintenance.Tuples = true, int64(rows)
		if len(r.target.columns) == 0 {
			maintenance.Maintenance.Modifications = 0
		}
		if store != maintenance {
			store.Maintenance.Scanned, store.Maintenance.Tuples = true, int64(rows)
			if len(r.target.columns) == 0 {
				store.Maintenance.Modifications = 0
			}
		}
		var dead int64
		for _, segment := range r.segments {
			if segment.relation.Address() == r.relation.Address() {
				dead = segment.dead
				break
			}
		}
		r.s.RecordStatsMaintenance(r.auth.Ref(r.relation.Address()), true, r.started, completed, int64(rows), dead, len(r.target.columns) == 0)
	}
	if final {
		r.clearInheritedLocked(metadata)
	}
	r.installLocked(metadata, original, final)
	return nil
}

func (r *analyzeRun) clearInheritedLocked(metadata *storage.RelationMetadata) {
	metadata.InheritedScanned, metadata.InheritedTuples = false, 0
	clear := func(columns map[storage.StatisticsKey]*storage.ColumnStatistics) {
		for key := range columns {
			if key.Inherited {
				if r.delta == nil {
					r.delta = make(map[storage.StatisticsKey]*storage.ColumnStatistics)
				}
				r.delta[key] = nil
			}
		}
	}
	if metadata.Statistics != nil {
		clear(metadata.Statistics.Columns)
	}
	if pending := r.s.PendingCatalog(); pending != nil {
		clear(pending.Statistics[r.relation.Address()].Columns)
	}
}

func (r *analyzeRun) publishFinal(population float64) error {
	if !validForeignEstimate(population) {
		return fmt.Errorf("stuntgres: invalid inherited ANALYZE population")
	}
	r.e.Mu.Lock()
	defer r.e.Mu.Unlock()
	if err := r.authorityLocked(); err != nil {
		return err
	}
	metadata, original := r.metadataLocked()
	metadata.InheritedScanned, metadata.InheritedTuples = true, population
	if r.relation.Partition != nil {
		r.forgetImportedLocked(metadata, storage.RelationTuples)
		// There is no local heap scan or second integer population authority.
		metadata.Scanned, metadata.Tuples = false, 0
		completed := time.Now().UTC().Truncate(time.Microsecond)
		metadata.AnalyzeCount++
		metadata.LastAnalyze = completed
		r.s.RecordStatsMaintenance(r.auth.Ref(r.relation.Address()), true, r.started, completed, 0, 0, len(r.target.columns) == 0)
	}
	r.installLocked(metadata, original, true)
	return nil
}
