package engine

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

func (s *StatementScope) validateForeignActionLocked(e *Engine, ref ids.ObjectRef) error {
	if err := s.validateLockedRelation(e); err != nil {
		return err
	}
	admission := s.foreignActionsOrZero().Admissions[ref.Address]
	if admission == nil || admission.Ref != ref || admission.Tx != s.txRef || s.owner.Ref != s.txRef {
		return errors.New("stuntgres: missing internal action admission")
	}
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() || ref.Epoch != s.txRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	current, ok := e.privateCatalogLocked(s.owner.Catalog).Relation(ref.Address)
	if !ok || !index.SameRelationRowShape(admission.Relation, current) || e.privateStorageLocked(s.owner.Catalog, ref.Address) != admission.Storage {
		return ids.ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(s.owner.Catalog)
	for _, captured := range admission.Jobs {
		if err := dml.RefreshForeignActionJob(view, &captured); err != nil {
			return err
		}
	}
	return nil
}

func (s *StatementScope) writeAuthorizationLocked(e *Engine) (AuthorizationView, error) {
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		return auth, err
	}
	// Keep the actual invoker capture in the statement slot. Rule relation
	// authority and RI owner substitution remain local to the returned view.
	s.routineAuth = auth
	if s.ruleAction.auth != nil {
		auth.State.CurrentUser = s.ruleAction.auth.State.CurrentUser
		if err = auth.Roles.ValidatePrincipal(auth.State.CurrentUser); err != nil {
			return AuthorizationView{}, err
		}
	}
	if s.foreignActionAuth != nil {
		auth.State.CurrentUser = s.foreignActionAuth.State.CurrentUser
		if err = auth.Roles.ValidatePrincipal(auth.State.CurrentUser); err != nil {
			return AuthorizationView{}, err
		}
	}
	return auth, nil
}

func (s *StatementScope) admitForeignAction(ref ids.ObjectRef, job *dml.ForeignKeyJob, writable bool) error {
	e := s.engine()
	required := lock.RowShare | lock.RowExclusive
	if writable {
		required = lock.RowExclusive
	}
	held := e.locks.Holds(s.ticket, lock.Resource{ObjectRef: ref}, required)
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.validateLockedRelation(e); err != nil {
		return err
	}
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() || ref.Epoch != s.txRef.Epoch() || ref.Address != job.Child.Address() {
		return ids.ErrCatalogReplaced
	}
	if !held {
		return errors.New("stuntgres: internal action admission requires its relation grant")
	}
	current, ok := e.privateCatalogLocked(s.owner.Catalog).Relation(ref.Address)
	storage := e.privateStorageLocked(s.owner.Catalog, ref.Address)
	if !ok || !index.SameRelationRowShape(current, job.Child) || storage == nil {
		return ids.ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(s.owner.Catalog)
	if err := dml.RefreshForeignActionJob(view, job); err != nil {
		return err
	}
	if s.foreignActionsOrZero().Admissions == nil {
		s.foreignActions().Admissions = make(map[ids.ObjectAddress]*dml.ForeignActionAdmission)
	}
	if previous := s.foreignActionsOrZero().Admissions[ref.Address]; previous != nil {
		if err := s.validateForeignActionLocked(e, ref); err != nil {
			return err
		}
		present := false
		for _, captured := range previous.Jobs {
			present = present || captured.Definition.OID == job.Definition.OID
		}
		if !present {
			previous.Jobs = append(previous.Jobs, *job)
		}
		previous.Writable = previous.Writable || writable
		return nil
	}
	s.foreignActions().Admissions[ref.Address] = &dml.ForeignActionAdmission{Ref: ref, Storage: storage, Relation: current, Tx: s.txRef, Jobs: []dml.ForeignKeyJob{*job}, Writable: writable}
	return nil
}

// Locate the latest live physical row after the row grant, including this
// statement's replacements. Stable snapshots use the ordinary conflict seam.
func (s *StatementScope) foreignActionRow(ref ids.ObjectRef, candidate storage.RowVersion) (storage.RowVersion, bool, error) {
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.validateForeignActionLocked(e, ref); err != nil {
		return storage.RowVersion{}, false, err
	}
	stored := s.foreignActionsOrZero().Admissions[ref.Address].Storage
	if index.UniqueRowDeleted(candidate.Row, s.ownDeletes[ref.Address], s.stage.deletes[ref.Address]) {
		return storage.RowVersion{}, false, nil
	}
	for _, rows := range [][]storage.RowVersion{s.stage.rows[ref.Address], s.ownWrites[ref.Address], stored.Versions} {
		row, _, found := storage.RowByID(rows, candidate.Row, -1)
		if !found {
			continue
		}
		if err := visibility.RepeatableConflict(s.owner.Isolation.Stable(), s.owner.Frontier, row, stored.Ends); err != nil {
			return storage.RowVersion{}, false, err
		}
		return row, visibility.LiveVersion(row, e.visibility.CommitSeq(), stored.Ends, nil), nil
	}
	return storage.RowVersion{}, false, fmt.Errorf("stuntgres: missing internal action predecessor")
}

func (w *WriteStage) deleteForeignAction(ref ids.ObjectRef, old storage.RowVersion) (err error) {
	s := w.scope
	defer func() {
		if err == nil {
			err = s.flushPhysicalEvents()
		} else {
			s.multiEvents = nil
		}
	}()
	e := s.engine()
	if !e.locks.Holds(s.ticket, lock.Resource{ObjectRef: ref, Row: old.Row}, lock.ForUpdate) {
		return errors.New("stuntgres: internal action deletion requires its row grant")
	}
	s.owner.Mu.Lock()
	defer s.owner.Mu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.validateForeignActionLocked(e, ref); err != nil {
		return err
	}
	if !s.foreignActionsOrZero().Admissions[ref.Address].Writable {
		return errors.New("stuntgres: read-only internal action admission")
	}
	if !s.worked || s.owner.State == session.TxIdle || s.owner.State == session.TxAborted {
		return errors.New("stuntgres: inactive internal action stage")
	}
	if index.UniqueRowDeleted(old.Row, s.ownDeletes[ref.Address], w.deletes[ref.Address]) {
		return nil
	}
	stored := s.foreignActionsOrZero().Admissions[ref.Address].Storage
	candidate := visibility.DeleteCandidate{Ref: ref, Storage: stored, Row: old.Row, Version: old.Version, Base: old.Base, Own: old.Born == 0}
	if old.Born != 0 {
		candidate.Base = old.Version
	}
	if err := w.stageDeletionLocked(e, candidate, old); err != nil {
		return err
	}
	s.foreignActions().Admissions[ref.Address].Mutated = true
	s.foreignActions().Transitions = append(s.foreignActionsOrZero().Transitions, dml.ForeignActionTransition{Relation: ref.Address, Old: old, Owner: s.foreignActionsOrZero().Admissions[ref.Address].Relation.Owner})
	return nil
}

func (s *StatementScope) recordForeignActionReplacement(ref ids.ObjectRef, old, next storage.RowVersion) {
	if !s.foreignActionsOrZero().Settling {
		return
	}
	admission := s.foreignActionsOrZero().Admissions[ref.Address]
	admission.Mutated = true
	s.foreignActions().Transitions = append(s.foreignActionsOrZero().Transitions, dml.ForeignActionTransition{Relation: ref.Address, Old: old, Next: next, Owner: admission.Relation.Owner})
}

// foreignActions returns the statement's foreignActions slot, allocating it on first use.
func (s *StatementScope) foreignActions() *dml.ForeignActionScope {
	if s.foreignActionsSlot == nil {
		s.foreignActionsSlot = new(dml.ForeignActionScope)
	}
	return s.foreignActionsSlot
}

// foreignActionsOrZero reads the foreignActions slot; a nil slot reads as zero.
func (s *StatementScope) foreignActionsOrZero() dml.ForeignActionScope {
	if s.foreignActionsSlot == nil {
		return dml.ForeignActionScope{}
	}
	return *s.foreignActionsSlot
}
