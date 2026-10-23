package engine

import (
	"fmt"
	"math"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"time"
)

func (e *Engine) captureVacuumLocked(s *StatementScope, target vacuumHeapTarget) (VacuumCapture, bool, error) {
	var c VacuumCapture
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
	allowed, err := VacuumAllowed(s.op.Context(), auth.View, auth, d)
	if err != nil {
		return c, false, err
	}
	if !allowed {
		return c, false, nil
	}
	cutoff, err := e.visibility.VacuumCutoffLocked()
	if err != nil {
		return c, false, err
	}
	c = VacuumCapture{Rows: target.store.Versions, Ends: target.store.Ends, Generation: target.store.Generation, Frontier: e.visibility.CommitSeq(), Cutoff: cutoff, Retire: e.visibility.VacuumRetirementAllowedLocked(s.txRef), Catalog: e.catalog.state, Relation: d}
	return c, true, nil
}

func (e *Engine) vacuumRelation(s *StatementScope, selected VacuumTarget, o VacuumOptions) error {
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
	if selected.Ref.Domain() != domainOf(e) || selected.Ref.Epoch != auth.Catalog.Epoch() {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	d, exists := auth.Catalog.Relation(selected.Ref.Address)
	if !exists {
		e.mu.Unlock()
		if selected.Name != "" {
			vacuumNotice(s, VacuumMissingWarning(selected.Name))
		}
		return nil
	}
	// These are work-phase gates, after the actual SUE grant and fresh ACL.
	work, warning, err := VacuumWork(s.op.Context(), auth.View, auth, auth.session.SessionToken, d, o.ProcessMain)
	if err != nil || !work {
		e.mu.Unlock()
		if warning != (VacuumWarning{}) {
			vacuumNotice(s, warning)
		}
		return err
	}
	store := e.storage.Relations[d.Address()]
	if store == nil {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	cutoff, err := e.visibility.VacuumCutoffLocked()
	if err != nil {
		e.mu.Unlock()
		return err
	}
	// Bind current storage/shape only now. Detached work still uses the same
	// strict epoch, store, shape, root, index, rights and final-publication fences.
	target := vacuumHeapTarget{selected.Ref, d, store}
	first := VacuumCapture{Rows: store.Versions, Ends: store.Ends, Generation: store.Generation, Frontier: e.visibility.CommitSeq(), Cutoff: cutoff, Retire: e.visibility.VacuumRetirementAllowedLocked(s.txRef), Catalog: e.catalog.state, Relation: d}
	first.CaptureIndexesLocked(e.catalog.owner(), e.storage.IndexRoots, true)
	if s.op.Session.SessionToken.ID() > math.MaxInt32 {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: vacuum session identity exceeds integer range")
	}
	if e.vacuumProgress == nil {
		e.vacuumProgress = make(map[ids.TxRef]vacuumProgress)
	}
	e.vacuumProgress[s.txRef] = vacuumProgress{fact: pgcatalog.VacuumProgressFact{PID: int64(s.op.Session.SessionToken.ID()), Database: VacuumDatabaseOID(auth.Catalog), DatabaseName: s.op.Session.DatabaseName, Relation: target.ref.Address.ObjectID, Phase: "initializing", IndexesTotal: int64(len(first.Indexes))}, owner: auth.State.Authenticated}
	e.mu.Unlock()
	defer e.endVacuumProgress(s.txRef)
	phase := func(name string, total, processed int64) {
		e.mu.Lock()
		e.vacuumPhaseLocked(s, name, total, processed)
		e.mu.Unlock()
	}
	capture := first
	for {
		pass, err := PrepareVacuumPass(s.op.Context(), capture, target.store, phase)
		if err != nil {
			return err
		}
		completed, err := vacuumCompletion(s)
		if err != nil {
			return err
		}
		e.mu.Lock()
		current, allowed, err := e.captureVacuumLocked(s, target)
		if err != nil || !allowed {
			e.mu.Unlock()
			if err == nil {
				vacuumNotice(s, VacuumPermissionWarning(target.relation.Name()))
			}
			return err
		}
		current.CaptureIndexesLocked(e.catalog.owner(), e.storage.IndexRoots, false)
		if !capture.SameLocked(current, &e.storage, pass.Catalog != nil) {
			current.CaptureIndexesLocked(e.catalog.owner(), e.storage.IndexRoots, true)
			capture = current
			e.mu.Unlock()
			continue
		}
		// All detached work and every failure boundary precede the first install.
		if err := s.op.Context().Err(); err != nil {
			e.mu.Unlock()
			return err
		}
		target.store.Versions, target.store.Ends, target.store.Generation = pass.Candidate.Versions, pass.Candidate.Ends, pass.Generation
		for address, root := range pass.Roots {
			e.storage.InstallIndexRootLocked(address, root)
		}
		if pass.Catalog != nil {
			e.catalog.state = pass.Catalog
		}
		target.store.Maintenance.RecordVacuum(pass.Candidate, completed)
		e.mu.Unlock()
		if o.Verbose {
			message := VacuumReport(catalog.NewRead(e.catalog.owner(), capture.Catalog), capture.Relation, pass.Candidate)
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
