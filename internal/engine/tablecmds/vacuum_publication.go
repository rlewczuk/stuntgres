package tablecmds

import (
	"fmt"
	"math"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqlstate"
	"time"
)

func captureVacuumLocked(e *core.Engine, s *core.StatementScope, target vacuumHeapTarget) (table.VacuumCapture, bool, error) {
	var c table.VacuumCapture
	if err := vacuumDatabasePreflightLocked(e, s); err != nil {
		return c, false, err
	}
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		return c, false, err
	}
	d, ok := auth.Catalog.Relation(target.ref.Address)
	if !ok || target.ref.Epoch != auth.Catalog.Epoch() || d.Name() != target.relation.Name() || d.Namespace() != target.relation.Namespace() || !index.SameRelationRowShape(d, target.relation) || e.Storage.Relations[d.Address()] != target.store {
		return c, false, ids.ErrCatalogReplaced
	}
	allowed, err := table.VacuumAllowed(s.Op.Context(), auth.View, auth, d)
	if err != nil {
		return c, false, err
	}
	if !allowed {
		return c, false, nil
	}
	cutoff, err := e.Visibility.VacuumCutoffLocked()
	if err != nil {
		return c, false, err
	}
	c = table.VacuumCapture{Rows: target.store.Versions, Ends: target.store.Ends, Generation: target.store.Generation, Frontier: e.Visibility.CommitSeq(), Cutoff: cutoff, Retire: e.Visibility.VacuumRetirementAllowedLocked(s.TxRef), Catalog: e.Catalog.State, Relation: d}
	return c, true, nil
}

func vacuumRelation(e *core.Engine, s *core.StatementScope, selected table.VacuumTarget, o table.VacuumOptions) error {
	e.Mu.Lock()
	if err := vacuumDatabasePreflightLocked(e, s); err != nil {
		e.Mu.Unlock()
		return err
	}
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	if selected.Ref.Domain() != core.DomainOf(e) || selected.Ref.Epoch != auth.Catalog.Epoch() {
		e.Mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	d, exists := auth.Catalog.Relation(selected.Ref.Address)
	if !exists {
		e.Mu.Unlock()
		if selected.Name != "" {
			vacuumNotice(s, table.VacuumMissingWarning(selected.Name))
		}
		return nil
	}
	// These are work-phase gates, after the actual SUE grant and fresh ACL.
	work, warning, err := table.VacuumWork(s.Op.Context(), auth.View, auth, auth.Session.SessionToken, d, o.ProcessMain)
	if err != nil || !work {
		e.Mu.Unlock()
		if warning != (table.VacuumWarning{}) {
			vacuumNotice(s, warning)
		}
		return err
	}
	store := e.Storage.Relations[d.Address()]
	if store == nil {
		e.Mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	cutoff, err := e.Visibility.VacuumCutoffLocked()
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	// Bind current storage/shape only now. Detached work still uses the same
	// strict epoch, store, shape, root, index, rights and final-publication fences.
	target := vacuumHeapTarget{selected.Ref, d, store}
	first := table.VacuumCapture{Rows: store.Versions, Ends: store.Ends, Generation: store.Generation, Frontier: e.Visibility.CommitSeq(), Cutoff: cutoff, Retire: e.Visibility.VacuumRetirementAllowedLocked(s.TxRef), Catalog: e.Catalog.State, Relation: d}
	first.CaptureIndexesLocked(&e.Catalog.Owner, e.Storage.IndexRoots, true)
	if s.Op.Session.SessionToken.ID() > math.MaxInt32 {
		e.Mu.Unlock()
		return fmt.Errorf("stuntgres: vacuum session identity exceeds integer range")
	}
	if e.VacuumProgress == nil {
		e.VacuumProgress = make(map[ids.TxRef]core.VacuumProgress)
	}
	e.VacuumProgress[s.TxRef] = core.VacuumProgress{Fact: pgcatalog.VacuumProgressFact{PID: int64(s.Op.Session.SessionToken.ID()), Database: table.VacuumDatabaseOID(auth.Catalog), DatabaseName: s.Op.Session.DatabaseName, Relation: target.ref.Address.ObjectID, Phase: "initializing"}, Owner: auth.State.Authenticated}
	e.Mu.Unlock()
	defer endVacuumProgress(e, s.TxRef)
	capture := first
	var cycles int64
	for {
		baseCycles := cycles
		pass, err := table.PrepareVacuumPass(s.Op.Context(), capture, target.store, func(progress table.VacuumProgress) {
			cycles = baseCycles + progress.IndexCycles
			e.Mu.Lock()
			vacuumProgressLocked(e, s, progress, len(first.Rows), cycles)
			e.Mu.Unlock()
		})
		if err != nil {
			return err
		}
		completed, err := vacuumCompletion(s)
		if err != nil {
			return err
		}
		e.Mu.Lock()
		current, allowed, err := captureVacuumLocked(e, s, target)
		if err != nil || !allowed {
			e.Mu.Unlock()
			if err == nil {
				vacuumNotice(s, table.VacuumPermissionWarning(target.relation.Name()))
			}
			return err
		}
		current.CaptureIndexesLocked(&e.Catalog.Owner, e.Storage.IndexRoots, false)
		if !capture.SameLocked(current, &e.Storage, pass.Catalog != nil) {
			current.CaptureIndexesLocked(&e.Catalog.Owner, e.Storage.IndexRoots, true)
			capture = current
			e.Mu.Unlock()
			continue
		}
		// All detached work and every failure boundary precede the first install.
		if err := s.Op.Context().Err(); err != nil {
			e.Mu.Unlock()
			return err
		}
		target.store.Versions, target.store.Ends, target.store.Generation = pass.Candidate.Versions, pass.Candidate.Ends, pass.Generation
		for address, root := range pass.Roots {
			e.Storage.InstallIndexRootLocked(address, root)
		}
		if pass.Catalog != nil {
			e.Catalog.PublishLocked(pass.Catalog)
		}
		target.store.Maintenance.RecordVacuum(pass.Candidate, completed)
		e.Mu.Unlock()
		if o.Verbose {
			message := table.VacuumReport(catalog.NewRead(&e.Catalog.Owner, capture.Catalog), capture.Relation, pass.Candidate)
			s.Op.EmitNotice(session.LevelInfo, sqlstate.Error{Severity: "INFO", SeverityUnlocalized: "INFO", Code: "00000", Message: message})
		}
		return nil
	}
}

// Completion timestamp and cancellation are prepared outside publication locks.
func vacuumCompletion(s *core.StatementScope) (time.Time, error) {
	completed := time.Now().UTC().Truncate(time.Microsecond)
	return completed, s.Op.Context().Err()
}
