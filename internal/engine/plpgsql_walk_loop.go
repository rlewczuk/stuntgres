package engine

import (
	"errors"
	"fmt"

	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// executePLLoop keeps its name: the reference loop cancellation fixture
// waits for this frame before cancelling.
func (r *plWalk) executePLLoop(loop *proc.PLBlock) (sqltype.Value, proc.PLFlow, error) {
	f := r.Frame
	f.Enter(loop)
	defer f.Leave(loop)
	for {
		// An empty body and a continue-only body must both remain cancellable.
		if err := r.Host.Context().Err(); err != nil {
			return sqltype.Value{}, proc.PLFlow{}, err
		}
		value, flow, err := r.Statements(loop.Statements)
		if err != nil || flow.Kind == proc.PLReturn {
			return value, flow, err
		}
		if flow.Kind == proc.PLNormal {
			continue
		}
		if flow.Target != loop.Control {
			return value, flow, nil
		}
		if flow.Kind == proc.PLExit {
			return sqltype.Value{}, proc.PLFlow{}, nil
		}
		if flow.Kind != proc.PLContinue {
			return sqltype.Value{}, proc.PLFlow{}, fmt.Errorf("stuntgres: invalid PL loop flow")
		}
	}
}

func (r *plWalk) transfer(in proc.PLInstruction) (sqltype.Value, proc.PLFlow, error) {
	raw := in.Syntax
	action, kind := "at EXIT", proc.PLExit
	if raw.Transfer.Continue {
		action, kind = "at CONTINUE", proc.PLContinue
	}
	if in.Query != nil {
		at := proc.PLOrigin{Position: raw.Pos, Action: action}
		value, _, err := r.Host.Evaluate(r.Frame, in.Query, at)
		expressionError := err != nil
		if err == nil {
			boolean, ok := r.Host.LookupType(sqltype.Bool)
			if !ok {
				return sqltype.Value{}, proc.PLFlow{}, fmt.Errorf("stuntgres: missing PL condition Boolean type")
			}
			value, err = r.Host.Convert(value, boolean.Usage(), at)
		}
		if err != nil {
			return sqltype.Value{}, proc.PLFlow{}, proc.PLRuntimeError(err, r.Definition, r.Host.Identity, raw.ExpressionStart, raw.ExpressionEnd, raw.Pos, action, expressionError)
		}
		if value.IsNull() {
			return sqltype.Value{}, proc.PLFlow{}, nil
		}
		truth, err := value.Boolean()
		if err != nil {
			return sqltype.Value{}, proc.PLFlow{}, err
		}
		if !truth {
			return sqltype.Value{}, proc.PLFlow{}, nil
		}
	}
	return sqltype.Value{}, proc.PLFlow{Kind: kind, Target: in.Transfer}, nil
}

func (r *plWalk) conditional(instruction proc.PLInstruction) (sqltype.Value, proc.PLFlow, error) {
	f := r.Frame
	checked, raw, action := instruction.If, instruction.Syntax.If, "at IF"
	isCase := instruction.Case != nil
	if isCase {
		checked, raw, action = instruction.Case, instruction.Syntax.Case, "at CASE"
	}
	at := proc.PLOrigin{Position: instruction.Syntax.Pos, Action: action}
	if checked.Search != nil {
		if err := r.Host.Context().Err(); err != nil {
			return sqltype.Value{}, proc.PLFlow{}, err
		}
		value, usage, err := r.Host.Evaluate(f, checked.Search, at)
		if err != nil {
			return sqltype.Value{}, proc.PLFlow{}, proc.PLRuntimeError(err, r.Definition, r.Host.Identity, raw.Search.Start, raw.Search.End, instruction.Syntax.Pos, action, true)
		}
		f.Searches[checked.SearchSlot] = proc.PLSearch{Value: value, Usage: usage}
		defer func() { f.Searches[checked.SearchSlot] = proc.PLSearch{} }()
	}
	boolean, ok := r.Host.LookupType(sqltype.Bool)
	if !ok {
		return sqltype.Value{}, proc.PLFlow{}, errors.New("stuntgres: missing PL condition Boolean type")
	}
	for i, branch := range checked.Arms {
		if err := r.Host.Context().Err(); err != nil {
			return sqltype.Value{}, proc.PLFlow{}, err
		}
		value, _, err := r.Host.Evaluate(f, branch.Query, at)
		expressionError := err != nil
		if err == nil {
			value, err = r.Host.Convert(value, boolean.Usage(), at)
		}
		if err != nil {
			arm := raw.Arms[i]
			first := arm.Conditions[0]
			if checked.Search != nil && expressionError {
				err = proc.PLCaseExpressionError(err, branch, first.Start)
			}
			return sqltype.Value{}, proc.PLFlow{}, proc.PLRuntimeError(err, r.Definition, r.Host.Identity, first.Start, arm.Conditions[len(arm.Conditions)-1].End, instruction.Syntax.Pos, action, expressionError && checked.Search == nil)
		}
		if value.IsNull() {
			continue
		}
		truth, err := value.Boolean()
		if err != nil {
			return sqltype.Value{}, proc.PLFlow{}, err
		}
		if truth {
			return r.Statements(branch.Statements)
		}
	}
	if isCase && !checked.HasElse {
		err := sqlError("20000", "case not found").(*sqlstate.Error)
		err.Hint = "CASE statement is missing ELSE part."
		return sqltype.Value{}, proc.PLFlow{}, proc.PLRuntimeError(err, r.Definition, r.Host.Identity, parser.Position{}, parser.Position{}, instruction.Syntax.Pos, action, false)
	}
	return r.Statements(checked.Else)
}
