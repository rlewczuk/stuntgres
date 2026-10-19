package engine

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqltype"
)

// This authority is private to one statement and one captured physical child.
// It never substitutes for the initiating target's admission or candidate view.
type foreignActionAdmission struct {
	ref               ids.ObjectRef
	storage           *relationStorage
	relation          *catalog.Relation
	tx                ids.TxRef
	jobs              []foreignKeyJob
	writable, mutated bool
}

type foreignActionTransition struct {
	relation  ids.ObjectAddress
	old, next rowVersion
	owner     authorization.PrincipalRef // zero means the initiating statement's actor
}

func (s *StatementScope) validateForeignActionLocked(e *Engine, ref ids.ObjectRef) error {
	if err := s.validateLockedRelation(e); err != nil {
		return err
	}
	admission := s.foreignActionAdmissions[ref.Address]
	if admission == nil || admission.ref != ref || admission.tx != s.txRef || s.owner.ref != s.txRef {
		return errors.New("stuntgres: missing internal action admission")
	}
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() || ref.Epoch != s.txRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	current, ok := e.privateCatalogLocked(s.owner.catalog).Relation(ref.Address)
	if !ok || !sameRelationRowShape(admission.relation, current) || e.privateStorageLocked(s.owner.catalog, ref.Address) != admission.storage {
		return ids.ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(s.owner.catalog)
	for _, captured := range admission.jobs {
		if err := refreshForeignActionJob(view, &captured); err != nil {
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
	if s.ruleActionAuth != nil {
		auth.state.CurrentUser = s.ruleActionAuth.state.CurrentUser
		if err = auth.roles.ValidatePrincipal(auth.state.CurrentUser); err != nil {
			return AuthorizationView{}, err
		}
	}
	if s.foreignActionAuth != nil {
		auth.state.CurrentUser = s.foreignActionAuth.state.CurrentUser
		if err = auth.roles.ValidatePrincipal(auth.state.CurrentUser); err != nil {
			return AuthorizationView{}, err
		}
	}
	return auth, nil
}

func (s *StatementScope) admitForeignAction(ref ids.ObjectRef, job *foreignKeyJob, writable bool) error {
	e := s.op.session.Engine()
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
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() || ref.Epoch != s.txRef.Epoch() || ref.Address != job.child.Address() {
		return ids.ErrCatalogReplaced
	}
	if !held {
		return errors.New("stuntgres: internal action admission requires its relation grant")
	}
	current, ok := e.privateCatalogLocked(s.owner.catalog).Relation(ref.Address)
	storage := e.privateStorageLocked(s.owner.catalog, ref.Address)
	if !ok || !sameRelationRowShape(current, job.child) || storage == nil {
		return ids.ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(s.owner.catalog)
	if err := refreshForeignActionJob(view, job); err != nil {
		return err
	}
	if s.foreignActionAdmissions == nil {
		s.foreignActionAdmissions = make(map[ids.ObjectAddress]*foreignActionAdmission)
	}
	if previous := s.foreignActionAdmissions[ref.Address]; previous != nil {
		if err := s.validateForeignActionLocked(e, ref); err != nil {
			return err
		}
		present := false
		for _, captured := range previous.jobs {
			present = present || captured.definition.OID == job.definition.OID
		}
		if !present {
			previous.jobs = append(previous.jobs, *job)
		}
		previous.writable = previous.writable || writable
		return nil
	}
	s.foreignActionAdmissions[ref.Address] = &foreignActionAdmission{ref: ref, storage: storage, relation: current, tx: s.txRef, jobs: []foreignKeyJob{*job}, writable: writable}
	return nil
}

// Locate the latest live physical row after the row grant, including this
// statement's replacements. Stable snapshots use the ordinary conflict seam.
func (s *StatementScope) foreignActionRow(ref ids.ObjectRef, candidate rowVersion) (rowVersion, bool, error) {
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.validateForeignActionLocked(e, ref); err != nil {
		return rowVersion{}, false, err
	}
	storage := s.foreignActionAdmissions[ref.Address].storage
	if uniqueRowDeleted(candidate.Row, s.ownDeletes[ref.Address], s.stage.deletes[ref.Address]) {
		return rowVersion{}, false, nil
	}
	for _, rows := range [][]rowVersion{s.stage.rows[ref.Address], s.ownWrites[ref.Address], storage.Versions} {
		row, _, found := rowByID(rows, candidate.Row, -1)
		if !found {
			continue
		}
		if err := s.repeatableConflict(row, storage.Ends); err != nil {
			return rowVersion{}, false, err
		}
		return row, liveVersion(row, e.commitSeq, storage.Ends, nil), nil
	}
	return rowVersion{}, false, fmt.Errorf("stuntgres: missing internal action predecessor")
}

func (w *WriteStage) deleteForeignAction(ref ids.ObjectRef, old rowVersion) (err error) {
	s := w.scope
	defer func() {
		if err == nil {
			err = s.flushPhysicalEvents()
		} else {
			s.multiEvents = nil
		}
	}()
	e := s.op.session.Engine()
	if !e.locks.Holds(s.ticket, lock.Resource{ObjectRef: ref, Row: old.Row}, lock.ForUpdate) {
		return errors.New("stuntgres: internal action deletion requires its row grant")
	}
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.validateForeignActionLocked(e, ref); err != nil {
		return err
	}
	if !s.foreignActionAdmissions[ref.Address].writable {
		return errors.New("stuntgres: read-only internal action admission")
	}
	if !s.worked || s.owner.state == TxIdle || s.owner.state == TxAborted {
		return errors.New("stuntgres: inactive internal action stage")
	}
	if uniqueRowDeleted(old.Row, s.ownDeletes[ref.Address], w.deletes[ref.Address]) {
		return nil
	}
	storage := s.foreignActionAdmissions[ref.Address].storage
	candidate := deleteCandidate{ref: ref, storage: storage, row: old.Row, version: old.Version, base: old.Base, own: old.Born == 0}
	if old.Born != 0 {
		candidate.base = old.Version
	}
	if err := w.stageDeletionLocked(e, candidate, old); err != nil {
		return err
	}
	s.foreignActionAdmissions[ref.Address].mutated = true
	s.foreignActionTransitions = append(s.foreignActionTransitions, foreignActionTransition{relation: ref.Address, old: old, owner: s.foreignActionAdmissions[ref.Address].relation.Owner})
	return nil
}

func (s *StatementScope) recordForeignActionReplacement(ref ids.ObjectRef, old, next rowVersion) {
	if !s.foreignActionsSettling {
		return
	}
	admission := s.foreignActionAdmissions[ref.Address]
	admission.mutated = true
	s.foreignActionTransitions = append(s.foreignActionTransitions, foreignActionTransition{relation: ref.Address, old: old, next: next, owner: admission.relation.Owner})
}

func foreignActionOwner(auth AuthorizationView, owner authorization.PrincipalRef) AuthorizationView {
	if owner != (authorization.PrincipalRef{}) {
		auth.state.CurrentUser = owner
	}
	return auth
}

func foreignActionSameKey(comparisons []foreignKeyComparison, old, next []sqltype.Value) bool {
	for _, comparison := range comparisons {
		if !sqltype.SameDatum(old[comparison.parent], next[comparison.parent]) {
			return false
		}
	}
	return true
}

// Borrow both current and retained images under stable isolation. Current
// matches discover new/moved-in children; retained matches preserve moved-away
// successor conflicts. Neither image creates another snapshot or copies values.
func (s *StatementScope) captureForeignActionRows(ref ids.ObjectRef) (foreignKeyRows, foreignKeyRows, error) {
	rows, err := s.captureForeignKeyRows(ref, ids.ObjectAddress{})
	if err != nil {
		return rows, foreignKeyRows{}, err
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	var retained foreignKeyRows
	if stableIsolation(s.owner.isolation) && s.owner.snapshotSet {
		if root, exists := s.owner.roots[ref.Address]; exists {
			if root.storage != rows.storage {
				return foreignKeyRows{}, retained, ids.ErrCatalogReplaced
			}
			retained = rows
			retained.committed, retained.ends, retained.frontier = root.versions, root.ends, s.owner.frontier
		}
	}
	return rows, retained, nil
}
