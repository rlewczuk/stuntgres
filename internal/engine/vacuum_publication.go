package engine

import (
	"fmt"
	"math"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"time"
)

type vacuumCapture struct {
	rows       []storage.RowVersion
	ends       *storage.DeletionRoot
	generation uint64
	frontier   ids.CommitSeq
	cutoff     uint64
	retire     bool
	catalog    *catalog.State
	relation   *catalog.Relation
	indexes    map[ids.ObjectAddress]*storage.IndexRoot
	attached   int
	indexCount int
}

func (e *Engine) captureVacuumLocked(s *StatementScope, target vacuumHeapTarget) (vacuumCapture, bool, error) {
	var c vacuumCapture
	if err := e.vacuumDatabasePreflightLocked(s); err != nil {
		return c, false, err
	}
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		return c, false, err
	}
	d, ok := auth.Catalog.Relation(target.ref.Address)
	if !ok || target.ref.Epoch != auth.Catalog.Epoch() || d.Name() != target.relation.Name() || d.Namespace() != target.relation.Namespace() || !index.SameRelationRowShape(d, target.relation) || e.storage.Relations[d.Address()] != target.store {
		return c, false, ids.ErrCatalogReplaced
	}
	allowed, err := vacuumAllowed(s.op.Context(), auth, d)
	if err != nil {
		return c, false, err
	}
	if !allowed {
		return c, false, nil
	}
	cutoff, err := e.vacuumCutoffLocked()
	if err != nil {
		return c, false, err
	}
	c = vacuumCapture{rows: target.store.Versions, ends: target.store.Ends, generation: target.store.Generation, frontier: e.visibility.CommitSeq(), cutoff: cutoff, retire: e.vacuumRetirementAllowedLocked(s.txRef), catalog: e.catalog.state, relation: d}
	return c, true, nil
}

// Final preflight counts/checks existing roots without allocating a second
// snapshot map. Only a stale candidate needs a fresh detached root manifest.
func (e *Engine) captureVacuumIndexesLocked(c *vacuumCapture, collect bool) {
	c.attached, c.indexCount = 0, 0
	view := catalog.NewRead(e.catalog.owner(), c.catalog)
	for a, descriptor := range view.Descriptors() {
		if idx, ok := descriptor.(*catalog.Relation); ok && idx.SQLIndex != nil && idx.SQLIndex.Parent == c.relation.Address() {
			c.attached++
			if root := e.storage.IndexRoots[a]; root != nil {
				c.indexCount++
				if collect {
					if c.indexes == nil {
						c.indexes = make(map[ids.ObjectAddress]*storage.IndexRoot)
					}
					c.indexes[a] = root
				}
			}
		}
	}
}
func (e *Engine) vacuumRelation(s *StatementScope, selected vacuumTarget, o vacuumOptions) error {
	e.mu.Lock()
	if err := e.vacuumDatabasePreflightLocked(s); err != nil {
		e.mu.Unlock()
		return err
	}
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	if selected.ref.Domain() != domainOf(e) || selected.ref.Epoch != auth.Catalog.Epoch() {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	d, exists := auth.Catalog.Relation(selected.ref.Address)
	if !exists {
		e.mu.Unlock()
		if selected.name != "" {
			vacuumNotice(s, "42P01", fmt.Sprintf(`skipping vacuum of "%s" --- relation no longer exists`, selected.name))
		}
		return nil
	}
	allowed, err := vacuumAllowed(s.op.Context(), auth, d)
	if err != nil || !allowed {
		e.mu.Unlock()
		if err == nil {
			vacuumNotice(s, "01000", fmt.Sprintf(`permission denied to vacuum "%s", skipping it`, d.Name()))
		}
		return err
	}
	// These are work-phase gates, after the actual SUE grant and fresh ACL.
	// No heap storage, progress or main-work fact is required for a nonheap,
	// foreign TEMP, partitioned parent, or disabled main processing.
	if !vacuumHeap(d) && d.Partition == nil {
		e.mu.Unlock()
		vacuumNotice(s, "01000", fmt.Sprintf(`skipping "%s" --- cannot vacuum non-tables or special system tables`, d.Name()))
		return nil
	}
	if vacuumForeignTemp(auth, d) || d.Partition != nil || !o.processMain {
		e.mu.Unlock()
		return nil
	}
	store := e.storage.Relations[d.Address()]
	if store == nil {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	cutoff, err := e.vacuumCutoffLocked()
	if err != nil {
		e.mu.Unlock()
		return err
	}
	// Bind current storage/shape only now. Detached work still uses the same
	// strict epoch, store, shape, root, index, rights and final-publication fences.
	target := vacuumHeapTarget{selected.ref, d, store}
	first := vacuumCapture{rows: store.Versions, ends: store.Ends, generation: store.Generation, frontier: e.visibility.CommitSeq(), cutoff: cutoff, retire: e.vacuumRetirementAllowedLocked(s.txRef), catalog: e.catalog.state, relation: d}
	e.captureVacuumIndexesLocked(&first, true)
	if s.op.Session.SessionToken.ID() > math.MaxInt32 {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: vacuum session identity exceeds integer range")
	}
	if e.vacuumProgress == nil {
		e.vacuumProgress = make(map[ids.TxRef]vacuumProgress)
	}
	e.vacuumProgress[s.txRef] = vacuumProgress{fact: pgcatalog.VacuumProgressFact{PID: int64(s.op.Session.SessionToken.ID()), Database: vacuumDatabaseOID(auth.Catalog), DatabaseName: s.op.Session.DatabaseName, Relation: target.ref.Address.ObjectID, Phase: "initializing", IndexesTotal: int64(len(first.indexes))}, owner: auth.State.Authenticated}
	e.mu.Unlock()
	defer e.endVacuumProgress(s.txRef)
	capture := first
	for {
		e.mu.Lock()
		e.vacuumPhaseLocked(s, "scanning heap", int64(len(capture.indexes)), 0)
		e.mu.Unlock()
		candidate, err := storage.InspectVacuum(s.op.Context(), capture.rows, capture.ends, capture.frontier, capture.cutoff, capture.retire)
		if err != nil {
			return err
		}
		if candidate.Removed != 0 || candidate.Frozen != 0 {
			e.mu.Lock()
			e.vacuumPhaseLocked(s, "vacuuming heap", int64(len(capture.indexes)), 0)
			e.mu.Unlock()
			candidate, err = storage.MaterializeVacuum(s.op.Context(), capture.rows, capture.ends, capture.frontier, capture.cutoff, candidate)
			if err != nil {
				return err
			}
		}
		changed := !storage.SameVersionRoot(candidate.Versions, capture.rows) || candidate.Ends != capture.ends
		generation := capture.generation
		if changed {
			if generation == ^uint64(0) {
				return fmt.Errorf("stuntgres: storage generation exhausted")
			}
			generation++
		}
		var roots map[ids.ObjectAddress]*storage.IndexRoot
		processed := int64(0)
		indexWork := 0
		if changed {
			if candidate.Positions != nil {
				indexWork = len(capture.indexes)
			} else {
				for _, root := range capture.indexes {
					if root.Hash != nil {
						indexWork++
					}
				}
			}
		}
		if indexWork != 0 {
			e.mu.Lock()
			e.vacuumPhaseLocked(s, "vacuuming indexes", int64(len(capture.indexes)), 0)
			e.mu.Unlock()
			roots = make(map[ids.ObjectAddress]*storage.IndexRoot, indexWork)
			for address, root := range capture.indexes {
				if candidate.Positions == nil && root.Hash == nil {
					continue
				}
				remapped, err := storage.RemapVacuumIndex(s.op.Context(), root, capture.rows, candidate, target.store, generation)
				if err != nil {
					return err
				}
				roots[address] = remapped
				processed++
				e.mu.Lock()
				e.vacuumPhaseLocked(s, "vacuuming indexes", int64(len(capture.indexes)), processed)
				e.mu.Unlock()
			}
		}
		var nextCatalog *catalog.State
		if capture.relation.History.HasIndexes && capture.attached == 0 {
			nextCatalog = catalog.CloneState(capture.catalog)
			d := *capture.relation
			d.History.HasIndexes = false
			nextCatalog.SetDescriptor(d.Address(), &d)
		}
		e.mu.Lock()
		e.vacuumPhaseLocked(s, "performing final cleanup", int64(len(capture.indexes)), processed)
		e.mu.Unlock()
		completed, err := vacuumCompletion(s)
		if err != nil {
			return err
		}
		e.mu.Lock()
		current, allowed, err := e.captureVacuumLocked(s, target)
		if err != nil || !allowed {
			e.mu.Unlock()
			if err == nil {
				vacuumNotice(s, "01000", fmt.Sprintf(`permission denied to vacuum "%s", skipping it`, target.relation.Name()))
			}
			return err
		}
		e.captureVacuumIndexesLocked(&current, false)
		same := storage.SameVersionRoot(current.rows, capture.rows) && current.ends == capture.ends && current.generation == capture.generation && current.retire == capture.retire && current.cutoff >= capture.cutoff && current.attached == capture.attached && current.indexCount == capture.indexCount && e.storage.IndexRootsCurrent(capture.indexes)
		if nextCatalog != nil && current.catalog != capture.catalog {
			same = false
		}
		if !same {
			e.captureVacuumIndexesLocked(&current, true)
			capture = current
			e.mu.Unlock()
			continue
		}
		// All detached work and every failure boundary precede the first install.
		if err := s.op.Context().Err(); err != nil {
			e.mu.Unlock()
			return err
		}
		target.store.Versions, target.store.Ends, target.store.Generation = candidate.Versions, candidate.Ends, generation
		for address, root := range roots {
			e.storage.InstallIndexRootLocked(address, root)
		}
		if nextCatalog != nil {
			e.catalog.state = nextCatalog
		}
		m := &target.store.Maintenance
		m.Scanned, m.Live, m.Dead, m.LastVacuum = true, candidate.Live, candidate.Dead, completed
		m.Tuples = candidate.Live
		m.VacuumCount++
		m.InsertsSince = 0
		if candidate.Horizon != 0 && candidate.Horizon > m.FrozenXID {
			m.FrozenXID = candidate.Horizon
		}
		e.mu.Unlock()
		if o.verbose {
			ns, _ := catalog.NewRead(e.catalog.owner(), capture.catalog).Namespace(capture.relation.Namespace())
			qualified := catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(capture.relation.Name())
			message := fmt.Sprintf("vacuuming %s (native logical storage): %d live rows, %d retained dead versions, %d dead versions removed, %d versions frozen", qualified, candidate.Live, candidate.Dead, candidate.Removed, candidate.Frozen)
			s.op.EmitNotice(session.LevelInfo, sqlstate.Error{Severity: "INFO", SeverityUnlocalized: "INFO", Code: "00000", Message: message})
		}
		return nil
	}
}

// Completion timestamp and cancellation are prepared outside publication locks.
func vacuumCompletion(s *StatementScope) (time.Time, error) {
	completed := time.Now().UTC().Truncate(time.Microsecond)
	return completed, s.op.Context().Err()
}
