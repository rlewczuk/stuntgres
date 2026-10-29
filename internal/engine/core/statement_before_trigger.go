package core

import (
	"fmt"

	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/stored"
)

// BeforeStatement uses the admitted logical target, not a physical row grant.
// Query progress is marked before any eligibility decision or unlocked callback.
// TRUNCATE supplies its own deduplicated manifest and has no DML query dedup.
func (h *statementHost) BeforeStatement(ref ids.ObjectRef, event stored.TriggerEvent, targets []int32) error {
	s := h.scope()
	if err := s.Active(); err != nil {
		return err
	}
	e := s.Engine()
	mode := lock.RowExclusive
	if event == stored.TriggerTruncate {
		mode = lock.AccessExclusive
	}
	if !e.Locks.Holds(s.LockTicket, lock.Resource{ObjectRef: ref}, mode) {
		return fmt.Errorf("stuntgres: statement trigger target lost its lifecycle grant")
	}
	if event != stored.TriggerTruncate {
		f := s.afterFrame()
		key := afterStatementKey{ref, event}
		if _, fired := f.before[key]; fired {
			return nil
		}
		if f.before == nil {
			f.before = make(map[afterStatementKey]struct{})
		}
		f.before[key] = struct{}{}
	}
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	relation, ok := auth.Catalog.Relation(ref.Address)
	if !ok || auth.Ref(ref.Address) != ref {
		e.Mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	read, definitions := auth.Catalog, stored.TriggersOf(relation)
	e.Mu.Unlock()
	for _, definition := range definitions {
		p := definition.Parts()
		if p.Timing != stored.TriggerBefore || p.Level != stored.TriggerStatement {
			continue
		}
		if err := s.Active(); err != nil {
			return err
		}
		e.Mu.Lock()
		current, err := s.RoutineAuthorizationLocked()
		if err == nil && current.Ref(ref.Address) != ref {
			err = ids.ErrCatalogReplaced
		}
		e.Mu.Unlock()
		if err != nil {
			return err
		}
		if !definition.Mode().Fires(current.RuleReplica()) || !definition.Fires(event, targets) {
			continue
		}
		invocation := afterTriggerEvent{definition: definition, relation: relation, read: read, event: event, actor: s.ForeignActionAuth}
		pass, err := s.afterWhen(invocation, current)
		if err != nil {
			return err
		}
		if pass {
			if err := s.fireTrigger(invocation); err != nil {
				return err
			}
		}
	}
	return nil
}

// PL handlers have already discarded composite statement results. Native tuple
// pointers instead violate the executor protocol; never replace a helper error.
func beforeStatementNativeResult(result dml.BeforeResult, err error) error {
	if err != nil {
		return err
	}
	if result.Row != nil {
		return SQLError("39P01", "BEFORE STATEMENT trigger cannot return a value")
	}
	return nil
}
