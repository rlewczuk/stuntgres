package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// BeforeRow admits a relation-owned immutable chain. No evaluator runs while
// Engine.mu is held. Function EXECUTE was checked at installation, not firing;
// body expressions retain the ordinary effective DML/RI authority.
func (h *statementHost) BeforeRow(request dml.BeforeRow) (dml.BeforeResult, error) {
	s := h.scope()
	e := s.engine()
	result := dml.BeforeResult{Row: request.New, Proceed: true}
	if err := s.active(); err != nil {
		return result, err
	}
	if target, _ := e.locks.ReplacementGrants(s.ticket, request.Relation, nil); !target {
		return result, fmt.Errorf("stuntgres: trigger invocation requires the admitted physical write target")
	}
	key := dml.BeforeKey{Relation: request.Relation, Event: request.Event}
	command := h.Command()
	captured, found := command.BeforeCaptures[key]
	if !found {
		e.mu.Lock()
		auth, err := s.routineAuthorizationLocked()
		if err != nil {
			e.mu.Unlock()
			return result, err
		}
		relation, ok := auth.Catalog.Relation(request.Relation.Address)
		if !ok || auth.Ref(request.Relation.Address) != request.Relation {
			e.mu.Unlock()
			return result, fmt.Errorf("stuntgres: stale trigger target")
		}
		captured = dml.BeforeCapture{Relation: relation, Catalog: auth.Catalog, Definitions: stored.TriggersOf(relation)}
		e.mu.Unlock()
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
		if !definition.Fires(event, request.Targets) {
			continue
		}
		if err := s.op.Context().Err(); err != nil {
			return result, err
		}
		p := definition.Parts()
		e.mu.Lock()
		current, err := s.routineAuthorizationLocked()
		if err == nil {
			if current.Ref(p.Procedure.Address) != p.Procedure {
				err = fmt.Errorf("stuntgres: stale trigger function identity")
			} else {
				d, _ := current.Catalog.Get(p.Procedure.Address).(*catalog.Procedure)
				if d == nil || d.SQL == nil || !d.SQL.Trigger {
					err = fmt.Errorf("stuntgres: invalid installed trigger function")
				} else {
					_, err = exec.ValidateRoutineDescriptor(current.Catalog, current.Roles, d)
				}
			}
		}
		e.mu.Unlock()
		if err != nil {
			return result, err
		}
		if p.When != nil {
			old := exec.Context{EvalFrame: exec.EvalFrame{Scope: analyze.RulePseudoScope, Frame: 1, Row: request.Old}, EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: h, Catalog: read, Input: s.input, Output: s.output}}
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
		d := read.Get(p.Procedure.Address).(*catalog.Procedure)
		name := "INSERT"
		if request.Event == dml.BeforeUpdate {
			name = "UPDATE"
		} else if request.Event == dml.BeforeDelete {
			name = "DELETE"
		}
		rel, err := plTriggerRelation(read, relation)
		if err != nil {
			return result, err
		}
		frame, err := proc.NewPLTriggerFrame(d.SQL, rel, p.Name, name, p.Arguments, request.Old, result.Row)
		if err != nil {
			return result, err
		}
		if err = s.enterSQLRoutine(); err != nil {
			frame.Close()
			return result, err
		}
		value, err := s.invokePLTrigger(d, frame, read, relation)
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
