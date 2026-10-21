package engine

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// This authority is private to one statement and one captured physical child.
// It never substitutes for the initiating target's admission or candidate view.
type foreignActionAdmission struct {
	ref               ids.ObjectRef
	storage           *storage.Relation
	relation          *catalog.Relation
	tx                ids.TxRef
	jobs              []foreignKeyJob
	writable, mutated bool
}

type foreignActionTransition struct {
	relation  ids.ObjectAddress
	old, next storage.RowVersion
	owner     authorization.PrincipalRef // zero means the initiating statement's actor
}

func (s *StatementScope) validateForeignActionLocked(e *Engine, ref ids.ObjectRef) error {
	if err := s.validateLockedRelation(e); err != nil {
		return err
	}
	admission := s.foreignActionsOrZero().admissions[ref.Address]
	if admission == nil || admission.ref != ref || admission.tx != s.txRef || s.owner.Ref != s.txRef {
		return errors.New("stuntgres: missing internal action admission")
	}
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() || ref.Epoch != s.txRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	current, ok := e.privateCatalogLocked(s.owner.Catalog).Relation(ref.Address)
	if !ok || !sameRelationRowShape(admission.relation, current) || e.privateStorageLocked(s.owner.Catalog, ref.Address) != admission.storage {
		return ids.ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(s.owner.Catalog)
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
		auth.State.CurrentUser = s.ruleActionAuth.State.CurrentUser
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

func (s *StatementScope) admitForeignAction(ref ids.ObjectRef, job *foreignKeyJob, writable bool) error {
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
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() || ref.Epoch != s.txRef.Epoch() || ref.Address != job.child.Address() {
		return ids.ErrCatalogReplaced
	}
	if !held {
		return errors.New("stuntgres: internal action admission requires its relation grant")
	}
	current, ok := e.privateCatalogLocked(s.owner.Catalog).Relation(ref.Address)
	storage := e.privateStorageLocked(s.owner.Catalog, ref.Address)
	if !ok || !sameRelationRowShape(current, job.child) || storage == nil {
		return ids.ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(s.owner.Catalog)
	if err := refreshForeignActionJob(view, job); err != nil {
		return err
	}
	if s.foreignActionsOrZero().admissions == nil {
		s.foreignActions().admissions = make(map[ids.ObjectAddress]*foreignActionAdmission)
	}
	if previous := s.foreignActionsOrZero().admissions[ref.Address]; previous != nil {
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
	s.foreignActions().admissions[ref.Address] = &foreignActionAdmission{ref: ref, storage: storage, relation: current, tx: s.txRef, jobs: []foreignKeyJob{*job}, writable: writable}
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
	stored := s.foreignActionsOrZero().admissions[ref.Address].storage
	if uniqueRowDeleted(candidate.Row, s.ownDeletes[ref.Address], s.stage.deletes[ref.Address]) {
		return storage.RowVersion{}, false, nil
	}
	for _, rows := range [][]storage.RowVersion{s.stage.rows[ref.Address], s.ownWrites[ref.Address], stored.Versions} {
		row, _, found := storage.RowByID(rows, candidate.Row, -1)
		if !found {
			continue
		}
		if err := s.repeatableConflict(row, stored.Ends); err != nil {
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
	if !s.foreignActionsOrZero().admissions[ref.Address].writable {
		return errors.New("stuntgres: read-only internal action admission")
	}
	if !s.worked || s.owner.State == session.TxIdle || s.owner.State == session.TxAborted {
		return errors.New("stuntgres: inactive internal action stage")
	}
	if uniqueRowDeleted(old.Row, s.ownDeletes[ref.Address], w.deletes[ref.Address]) {
		return nil
	}
	stored := s.foreignActionsOrZero().admissions[ref.Address].storage
	candidate := visibility.DeleteCandidate{Ref: ref, Storage: stored, Row: old.Row, Version: old.Version, Base: old.Base, Own: old.Born == 0}
	if old.Born != 0 {
		candidate.Base = old.Version
	}
	if err := w.stageDeletionLocked(e, candidate, old); err != nil {
		return err
	}
	s.foreignActions().admissions[ref.Address].mutated = true
	s.foreignActions().transitions = append(s.foreignActionsOrZero().transitions, foreignActionTransition{relation: ref.Address, old: old, owner: s.foreignActionsOrZero().admissions[ref.Address].relation.Owner})
	return nil
}

func (s *StatementScope) recordForeignActionReplacement(ref ids.ObjectRef, old, next storage.RowVersion) {
	if !s.foreignActionsOrZero().settling {
		return
	}
	admission := s.foreignActionsOrZero().admissions[ref.Address]
	admission.mutated = true
	s.foreignActions().transitions = append(s.foreignActionsOrZero().transitions, foreignActionTransition{relation: ref.Address, old: old, next: next, owner: admission.relation.Owner})
}

func foreignActionOwner(auth AuthorizationView, owner authorization.PrincipalRef) AuthorizationView {
	if owner != (authorization.PrincipalRef{}) {
		auth.State.CurrentUser = owner
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
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	var retained foreignKeyRows
	if stableIsolation(s.owner.Isolation) && s.owner.SnapshotSet {
		if root, exists := s.owner.Roots[ref.Address]; exists {
			if root.Storage != rows.storage {
				return foreignKeyRows{}, retained, ids.ErrCatalogReplaced
			}
			retained = rows
			retained.committed, retained.ends, retained.frontier = root.Versions, root.Ends, s.owner.Frontier
		}
	}
	return rows, retained, nil
}

// foreignActionScope is the statement's foreign-key action slot: admitted
// referencing relations, row transitions, settling state, prepared check jobs
// and the foreign-key targets of CREATE/ALTER TABLE.
type foreignActionScope struct {
	admissions     map[ids.ObjectAddress]*foreignActionAdmission
	transitions    []foreignActionTransition
	settling       bool
	exclusionAdded bool // a new event admitted while settling actions
	jobs           []foreignKeyJob
	keyTargets     []ids.ObjectRef
	alterKeys      []alterForeignKeyAdmission
}

// foreignActions returns the statement's foreignActions slot, allocating it on first use.
func (s *StatementScope) foreignActions() *foreignActionScope {
	if s.foreignActionsSlot == nil {
		s.foreignActionsSlot = new(foreignActionScope)
	}
	return s.foreignActionsSlot
}

// foreignActionsOrZero reads the foreignActions slot; a nil slot reads as zero.
func (s *StatementScope) foreignActionsOrZero() foreignActionScope {
	if s.foreignActionsSlot == nil {
		return foreignActionScope{}
	}
	return *s.foreignActionsSlot
}
