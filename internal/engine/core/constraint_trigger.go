package core

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/stored"
)

func (s *StatementScope) constraintModes() index.ConstraintModes {
	if scope := s.IndexesOrZero(); scope.ConstraintsStarted {
		return scope.ConstraintModes
	}
	return ownerExtensionOrZero(s.Owner).constraintModes
}

// Admission, rather than Engine.Mu, owns modes and immutable vector membership.
func (s *StatementScope) beginConstraintEvents() *index.Scope {
	scope := s.indexes()
	if !scope.ConstraintsStarted {
		scope.ConstraintModes = ownerExtensionOrZero(s.Owner).constraintModes
		scope.ConstraintsStarted = true
	}
	return scope
}

// A claim is published before invoking SQL, so nested SET cannot replay it.
// Always reread the live vector and modes after callbacks and caught errors.
func (s *StatementScope) drainConstraintTriggers(all bool, selected map[ids.OID]bool) (bool, error) {
	fired := false
	for {
		modes := s.constraintModes()
		at := -1
		for i, event := range modes.Events {
			if event.UserTrigger == nil {
				continue
			}
			c := event.UserTrigger.Definition.Parts().Constraint
			if all || selected != nil && selected[event.Constraint] || !modes.Deferred(event.Constraint, c.Deferrable, c.InitiallyDeferred) {
				at = i
				break
			}
		}
		if at < 0 {
			return fired, nil
		}
		event := s.beginConstraintEvents().ClaimConstraintEvent(at)
		if event.Parent.Epoch != s.TxRef.Epoch() {
			return fired, ids.ErrCatalogReplaced
		}
		payload := event.UserTrigger
		accepted := afterTriggerEvent{definition: payload.Definition, relation: payload.Relation, read: payload.Read,
			event: payload.Event, old: payload.Old, next: payload.Next, targets: payload.Targets}
		if payload.Actor != (authorization.PrincipalRef{}) {
			e := s.Engine()
			e.Mu.Lock()
			auth, err := s.RoutineAuthorizationLocked()
			if err == nil {
				auth.State.CurrentUser = payload.Actor
				err = auth.Roles.ValidatePrincipal(payload.Actor)
			}
			e.Mu.Unlock()
			if err != nil {
				return fired, err
			}
			accepted.actor = &auth
		}
		if err := s.fireConstraintTrigger(accepted); err != nil {
			return fired, err
		}
		fired = true
		if err := s.completeCommandEffects(false, false); err != nil {
			return fired, err
		}
	}
}

// Accepted tuples and WHEN belong to admission; trigger identity and executable
// belong to the current catalog. A dropped OID is consumed, never rebound by name.
func (s *StatementScope) fireConstraintTrigger(event afterTriggerEvent) error {
	e := s.Engine()
	e.Mu.Lock()
	view := e.PrivateCatalogLocked(s.PendingCatalog())
	parent, exists := view.Relation(event.definition.Parts().Relation.Address)
	var live stored.Trigger
	if exists {
		for _, candidate := range stored.TriggersOf(parent) {
			if candidate.OID() == event.definition.OID() {
				live = candidate
				break
			}
		}
	}
	if live.OID() != 0 && s.CatalogAuthority == nil {
		// Capture before executable work, not from a later publication view.
		// Generic CREATE/REPLACE stages need this authority just as selected
		// ALTER stages do; it stays with the one outer terminal publisher.
		auth, err := s.RoutineAuthorizationLocked()
		if err != nil {
			e.Mu.Unlock()
			return err
		}
		s.CatalogAuthority = auth.CatalogAuthorityLocked(ids.ObjectRef{}, nil)
	}
	e.Mu.Unlock()
	if live.OID() == 0 {
		return nil
	}
	event.definition = live
	// This is call ancestry, not pending vector membership or query progress.
	// Exception rollback must never rewind it while these calls are still live.
	// Sequential events reuse backing; only a deeper nesting level may grow it.
	depth := len(s.activeConstraintTriggers)
	s.activeConstraintTriggers = append(s.activeConstraintTriggers, event.relation.Address())
	defer func() {
		clear(s.activeConstraintTriggers[depth:])
		s.activeConstraintTriggers = s.activeConstraintTriggers[:depth]
	}()
	return s.fireTrigger(event)
}
