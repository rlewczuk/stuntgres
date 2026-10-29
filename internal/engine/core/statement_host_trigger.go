package core

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// BeforeRow admits a relation-owned immutable chain. No evaluator runs while
// Engine.mu is held. Function EXECUTE was checked at installation, not firing;
// body expressions retain the ordinary effective DML/RI authority.
func (h *statementHost) BeforeRow(request dml.BeforeRow) (dml.BeforeResult, error) {
	s := h.scope()
	e := s.Engine()
	result := dml.BeforeResult{Row: request.New, Proceed: true}
	if err := s.Active(); err != nil {
		return result, err
	}
	timing, timingName := stored.TriggerBefore, "BEFORE"
	if request.Instead {
		w := s.ViewWrite
		if w == nil || !w.Instead || w.TriggerEvent != stored.TriggerEvent(request.Event) || w.Ref != request.Relation || s.LockedRef != w.Ref || s.LockedDescriptor != w.BaseRelation || s.LockedStorage != nil {
			return result, fmt.Errorf("stuntgres: trigger invocation requires the admitted logical view target")
		}
		if !e.Locks.Holds(s.LockTicket, lock.Resource{ObjectRef: request.Relation}, lock.RowExclusive) {
			return result, fmt.Errorf("stuntgres: logical trigger target lost its lifecycle grant")
		}
		timing, timingName = stored.TriggerInstead, "INSTEAD OF"
	} else if target, _ := e.Locks.ReplacementGrants(s.LockTicket, request.Relation, nil); !target {
		return result, fmt.Errorf("stuntgres: trigger invocation requires the admitted physical write target")
	}
	return h.invokeImmediateRow(request, timing, timingName)
}

// The guards above distinguish physical replacement authority from a logical
// lifecycle grant. Both paths share immutable command capture and live,
// unlocked executable admission, never a second trigger registry or queue.
func (h *statementHost) invokeImmediateRow(request dml.BeforeRow, timing stored.TriggerTiming, timingName string) (dml.BeforeResult, error) {
	s := h.scope()
	e := s.Engine()
	result := dml.BeforeResult{Row: request.New, Proceed: true}
	key := dml.BeforeKey{Relation: request.Relation, Event: request.Event, Instead: request.Instead}
	command := h.Command()
	captured, found := command.BeforeCaptures[key]
	if !found {
		e.Mu.Lock()
		auth, err := s.RoutineAuthorizationLocked()
		if err != nil {
			e.Mu.Unlock()
			return result, err
		}
		relation, ok := auth.Catalog.Relation(request.Relation.Address)
		if !ok || auth.Ref(request.Relation.Address) != request.Relation {
			e.Mu.Unlock()
			return result, fmt.Errorf("stuntgres: stale trigger target")
		}
		captured = dml.BeforeCapture{Relation: relation, Catalog: auth.Catalog, Definitions: stored.TriggersOf(relation)}
		if !request.Instead && (relation.RelationKind == catalog.ViewRelation || relation.RelationKind == catalog.TableRelation && e.PrivateStorageLocked(s.PendingCatalog(), request.Relation.Address) == nil) {
			e.Mu.Unlock()
			return result, fmt.Errorf("stuntgres: BEFORE invocation requires a physical relation")
		}
		e.Mu.Unlock()
		if command.BeforeCaptures == nil {
			command.BeforeCaptures = make(map[dml.BeforeKey]dml.BeforeCapture)
		}
		command.BeforeCaptures[key] = captured
	}
	relation, read, list := captured.Relation, captured.Catalog, captured.Definitions
	if len(list) == 0 {
		return result, nil
	}
	event := stored.TriggerEvent(request.Event)
	for _, definition := range list {
		if p := definition.Parts(); p.Timing != timing || p.Level != stored.TriggerRow {
			continue
		}
		if err := s.Op.Context().Err(); err != nil {
			return result, err
		}
		p := definition.Parts()
		e.Mu.Lock()
		current, err := s.RoutineAuthorizationLocked()
		// Earlier bodies in this row chain can change the effective role.
		// Decide eligibility before WHEN or function admission, not per chain.
		if err == nil && (!definition.Mode().Fires(current.RuleReplica()) || !definition.Fires(event, request.Targets)) {
			e.Mu.Unlock()
			continue
		}
		var d *catalog.Procedure
		if err == nil {
			if current.Ref(p.Procedure.Address) != p.Procedure {
				err = fmt.Errorf("stuntgres: stale trigger function identity")
			} else {
				d, _ = current.Catalog.Get(p.Procedure.Address).(*catalog.Procedure)
				if !catalog.SupportsDMLTrigger(d) {
					err = fmt.Errorf("stuntgres: invalid installed trigger function")
				} else {
					_, err = exec.ValidateRoutineDescriptor(current.Catalog, current.Roles, d)
					if err == nil && catalog.NativeDMLTrigger(d) && d.Builtin == nil {
						err = (stored.ProcedureProvider{}).Validate(current.Catalog, d)
					}
				}
			}
		}
		e.Mu.Unlock()
		if err != nil {
			return result, err
		}
		if p.When != nil {
			old := exec.Context{EvalFrame: exec.EvalFrame{Scope: analyze.RulePseudoScope, Frame: 1, Row: request.Old}, EvalStatement: exec.EvalStatement{Context: s.Op.Context(), Host: h, Catalog: read, Input: s.Input, Output: s.Output}}
			old.RoutineAuthority = &current
			old.RoutineRecipe = true
			old.RoutineChecks = p.Binding.Dependencies
			old.RoutineConsumer = relation
			next := old
			next.Frame = 2
			next.Row = result.Row
			next.Outer = &old
			if err := exec.AdmitExpression(&next, p.When, read); err != nil {
				return result, err
			}
			value, err := p.When.Eval(&next)
			if err != nil {
				return result, err
			}
			if value.IsNull() {
				continue
			}
			truth, err := value.Boolean()
			if err != nil {
				return result, err
			}
			if !truth {
				continue
			}
		}
		name := "INSERT"
		if request.Event == dml.BeforeUpdate {
			name = "UPDATE"
		} else if request.Event == dml.BeforeDelete {
			name = "DELETE"
		}
		if catalog.NativeDMLTrigger(d) {
			result, err = s.invokeNativeTrigger(d, relation, p, event, result.Row, request.Old, request.Targets)
			if err != nil || !result.Proceed {
				return result, err
			}
			continue
		}
		// WHEN runs unlocked and may reach SQL. Refresh executable authority
		// before private compilation, but never compile a skipped row.
		e.Mu.Lock()
		current, err = s.RoutineAuthorizationLocked()
		if err == nil {
			if current.Ref(p.Procedure.Address) != p.Procedure {
				err = fmt.Errorf("stuntgres: stale trigger function identity")
			} else {
				d, _ = current.Catalog.Get(p.Procedure.Address).(*catalog.Procedure)
				if !catalog.SupportsDMLTrigger(d) || catalog.NativeDMLTrigger(d) {
					err = fmt.Errorf("stuntgres: invalid installed trigger function")
				} else {
					_, err = exec.ValidateRoutineDescriptor(current.Catalog, current.Roles, d)
				}
			}
		}
		if err == nil {
			d, err = e.PreparePLRoutineLocked(s.Op, current, d)
		}
		e.Mu.Unlock()
		if err != nil {
			return result, err
		}
		rel, err := plTriggerRelation(read, relation)
		if err != nil {
			return result, err
		}
		frame, err := proc.NewPLTriggerFrame(d.SQL, rel, p.Name, name, timingName, "ROW", p.Arguments, request.Old, result.Row)
		if err != nil {
			return result, err
		}
		if err = s.enterSQLRoutine(); err != nil {
			frame.Close()
			return result, err
		}
		value, err := s.invokePLTrigger(d, frame, current.Catalog, relation)
		s.leaveSQLRoutine()
		frame.Close()
		if err != nil {
			return result, err
		}
		if value.IsNull() {
			result.Proceed = false
			return result, nil
		}
		if request.Event == dml.BeforeDelete {
			continue
		}
		row := make([]sqltype.Value, value.CompositeLen())
		for i := range row {
			row[i] = value.CompositeField(i)
		}
		result.Row = row
	}
	return result, nil
}
