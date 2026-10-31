package core

import (
	"errors"
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/visibility"
)

func (s *StatementScope) validateForeignActionLocked(e *Engine, ref ids.ObjectRef) error {
	if err := s.ValidateLockedRelation(e); err != nil {
		return err
	}
	admission := s.foreignActionsOrZero().Admissions[ref.Address]
	if admission == nil || admission.Ref != ref || admission.Tx != s.TxRef || s.Owner.Ref != s.TxRef {
		return errors.New("stuntgres: missing internal action admission")
	}
	if ref.Domain() != e.Catalog.Domain || ref.Epoch != e.Catalog.State.Epoch() || ref.Epoch != s.TxRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	current, ok := e.PrivateCatalogLocked(s.PendingCatalog()).Relation(ref.Address)
	if !ok || !index.SameRelationRowShape(admission.Relation, current) || e.PrivateStorageLocked(s.PendingCatalog(), ref.Address) != admission.Storage {
		return ids.ErrCatalogReplaced
	}
	view := e.PrivateCatalogLocked(s.PendingCatalog())
	for _, captured := range admission.Jobs {
		if err := dml.RefreshForeignActionJob(view, &captured); err != nil {
			return err
		}
	}
	return nil
}

func (s *StatementScope) writeAuthorizationLocked(e *Engine) (AuthorizationView, error) {
	// Query admission owns this immutable catalog independently of RoutineAuth,
	// which any current-authority consumer may refresh. SQL activation supplies
	// its own frontier and restores the enclosing query's slots on unwind.
	if s.largeObjectRead.State() == nil {
		s.largeObjectRead = s.Inquiry.CommentCatalog
		if s.largeObjectRead.State() == nil {
			// Utility/CHECK paths without query admission freeze before their
			// first current-authority operation, not after its catalog effects.
			s.largeObjectRead = e.PrivateCatalogLocked(s.PendingCatalog())
		}
	}
	s.captureLargeObjectOpeningLocked(e)
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		return auth, err
	}
	// Keep the actual invoker capture in the statement slot. Rule relation
	// authority and RI owner substitution remain local to the returned view.
	s.RoutineAuth = auth
	if s.ruleAction.Auth != nil {
		auth.State.CurrentUser = s.ruleAction.Auth.State.CurrentUser
		if err = auth.Roles.ValidatePrincipal(auth.State.CurrentUser); err != nil {
			return AuthorizationView{}, err
		}
	}
	if s.ForeignActionAuth != nil {
		auth.State.CurrentUser = s.ForeignActionAuth.State.CurrentUser
		if err = auth.Roles.ValidatePrincipal(auth.State.CurrentUser); err != nil {
			return AuthorizationView{}, err
		}
	}
	return auth, nil
}

func (s *StatementScope) admitForeignAction(ref ids.ObjectRef, job *dml.ForeignKeyJob, writable bool) error {
	e := s.Engine()
	required := lock.RowShare | lock.RowExclusive
	if writable {
		required = lock.RowExclusive
	}
	held := e.Locks.Holds(s.LockTicket, lock.Resource{ObjectRef: ref}, required)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := s.ValidateLockedRelation(e); err != nil {
		return err
	}
	if ref.Domain() != e.Catalog.Domain || ref.Epoch != e.Catalog.State.Epoch() || ref.Epoch != s.TxRef.Epoch() || ref.Address != job.Child.Address() {
		return ids.ErrCatalogReplaced
	}
	if !held {
		return errors.New("stuntgres: internal action admission requires its relation grant")
	}
	current, ok := e.PrivateCatalogLocked(s.PendingCatalog()).Relation(ref.Address)
	storage := e.PrivateStorageLocked(s.PendingCatalog(), ref.Address)
	if !ok || !index.SameRelationRowShape(current, job.Child) || storage == nil {
		return ids.ErrCatalogReplaced
	}
	view := e.PrivateCatalogLocked(s.PendingCatalog())
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
	s.foreignActions().Admissions[ref.Address] = &dml.ForeignActionAdmission{Ref: ref, Storage: storage, Relation: current, Tx: s.TxRef, Jobs: []dml.ForeignKeyJob{*job}, Writable: writable}
	return nil
}

// Locate the latest live physical row after the row grant, including this
// statement's replacements. Stable snapshots use the ordinary conflict seam.
func (s *StatementScope) foreignActionRow(ref ids.ObjectRef, candidate storage.RowVersion) (storage.RowVersion, bool, error) {
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := s.validateForeignActionLocked(e, ref); err != nil {
		return storage.RowVersion{}, false, err
	}
	stored := s.foreignActionsOrZero().Admissions[ref.Address].Storage
	if index.UniqueRowDeleted(candidate.Row, s.OwnDeletes[ref.Address], s.Stage.Deletes[ref.Address]) {
		return storage.RowVersion{}, false, nil
	}
	for _, rows := range [][]storage.RowVersion{s.Stage.Rows[ref.Address], s.OwnWrites[ref.Address], stored.Versions} {
		row, _, found := storage.RowByID(rows, candidate.Row, -1)
		if !found {
			continue
		}
		if err := visibility.RepeatableConflict(s.Owner.Isolation.Stable(), s.Owner.Frontier, row, stored.Ends); err != nil {
			return storage.RowVersion{}, false, err
		}
		return row, visibility.LiveVersion(row, e.Visibility.CommitSeq(), stored.Ends, nil), nil
	}
	return storage.RowVersion{}, false, fmt.Errorf("stuntgres: missing internal action predecessor")
}

func (w *WriteStage) deleteForeignAction(ref ids.ObjectRef, old storage.RowVersion) (err error) {
	s := w.scope
	afterDelete := false
	defer func() {
		if err == nil && afterDelete {
			err = s.WriteHost().AfterRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeDelete, Old: old.Values})
		}
	}()
	defer func() {
		if err == nil {
			err = s.flushPhysicalEvents()
		} else {
			s.multiEvents = nil
		}
	}()
	e := s.Engine()
	if !e.Locks.Holds(s.LockTicket, lock.Resource{ObjectRef: ref, Row: old.Row}, lock.ForUpdate) {
		return errors.New("stuntgres: internal action deletion requires its row grant")
	}
	s.Owner.Mu.Lock()
	defer s.Owner.Mu.Unlock()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := s.validateForeignActionLocked(e, ref); err != nil {
		return err
	}
	if !s.foreignActionsOrZero().Admissions[ref.Address].Writable {
		return errors.New("stuntgres: read-only internal action admission")
	}
	if !s.worked || s.Owner.State == session.TxIdle || s.Owner.State == session.TxAborted {
		return errors.New("stuntgres: inactive internal action stage")
	}
	if index.UniqueRowDeleted(old.Row, s.OwnDeletes[ref.Address], w.Deletes[ref.Address]) {
		return nil
	}
	childStorage := s.foreignActionsOrZero().Admissions[ref.Address].Storage
	candidate := visibility.DeleteCandidate{Ref: ref, Storage: childStorage, Row: old.Row, Version: old.Version, Base: old.Base, Own: old.Born == 0}
	if old.Born != 0 {
		candidate.Base = old.Version
	}
	if err := w.stageDeletionLocked(e, candidate, old); err != nil {
		return err
	}
	s.foreignActions().Admissions[ref.Address].Mutated = true
	relation, _ := e.PrivateCatalogLocked(s.PendingCatalog()).Relation(ref.Address)
	afterDelete = dml.HasRowTriggers(relation, stored.TriggerDelete, stored.TriggerAfter)
	return nil
}

// Row-at-a-time writers retain relevance in their existing physical relation
// binding (including each routed insertion group). Lanes without that binding
// use this allocation-free command value. Exact catalog changes invalidate both.
type foreignEffectAdmission struct {
	ref           ids.ObjectRef
	catalog       *catalog.State
	relevant      bool
	modesCaptured bool
	replica       bool
	suppressed    []dml.ForeignTriggerSuppression
}

func (s *StatementScope) foreignEffectRelevant(ref ids.ObjectRef, view catalog.Read) bool {
	if binding := s.UniqueBinding; binding != nil && binding.Ref == ref && binding.Catalog == view.State() {
		if parent, ok := view.Relation(ref.Address); ok && binding.Parent == parent {
			return binding.ConstraintEffectsRelevant
		}
	}
	if admission := s.foreignActionsOrZero().Admissions[ref.Address]; admission != nil && admission.Ref == ref {
		return true
	}
	cached := &s.effectAdmission
	if cached.ref == ref && cached.catalog == view.State() {
		return cached.relevant
	}
	*cached = foreignEffectAdmission{ref: ref, catalog: view.State()}
	if relation, ok := view.Relation(ref.Address); ok {
		if slices.ContainsFunc(relation.Constraints.ForeignKeys, func(f catalog.ForeignKeyConstraint) bool { return !f.NotEnforced }) || len(relation.Constraints.Exclusion) != 0 || slices.ContainsFunc(relation.Constraints.Unique, func(c catalog.UniqueConstraint) bool { return c.Deferrable }) {
			cached.relevant = true
			return true
		}
	}
	// Incoming FKs can use standalone unique indexes; constraint validity does
	// not disable enforcement on new writes. Use current catalog authority.
	for _, descriptor := range view.Descriptors() {
		if child, ok := descriptor.(*catalog.Relation); ok {
			for _, constraint := range child.Constraints.ForeignKeys {
				if !constraint.NotEnforced && constraint.Target == ref.Address {
					cached.relevant = true
					return true
				}
			}
		}
	}
	return false
}

func (s *StatementScope) recordForeignEffect(ref ids.ObjectRef, old, next storage.RowVersion) {
	owner := s.RoutineAuth.State.CurrentUser
	if s.ruleAction.Auth != nil {
		owner = s.ruleAction.Auth.State.CurrentUser
	}
	if s.ForeignActionAuth != nil {
		owner = s.ForeignActionAuth.State.CurrentUser
	}
	if s.foreignActionsOrZero().Settling {
		admission := s.foreignActionsOrZero().Admissions[ref.Address]
		if admission != nil {
			admission.Mutated = true
			owner = admission.Relation.Owner
		}
	}
	s.foreignActions().Transitions = append(s.foreignActionsOrZero().Transitions, dml.ForeignActionTransition{Relation: ref.Address, Old: old, Next: next, Owner: owner, Command: s.effectCommand, Suppressed: s.foreignTriggerSuppression(ref)})
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
