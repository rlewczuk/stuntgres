package proc

import (
	"errors"
	"fmt"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// executePLLoop keeps its name: the reference loop cancellation fixture
// waits for this frame before cancelling.
func (r *PLRun) executePLLoop(in PLInstruction) (sqltype.Value, PLFlow, error) {
	loop := in.Loop
	var boolean sqltype.Usage
	if in.While != nil {
		loop = in.While.Body
		typ, ok := r.Host.LookupType(sqltype.Bool)
		if !ok {
			return sqltype.Value{}, PLFlow{}, fmt.Errorf("stuntgres: missing PL condition Boolean type")
		}
		boolean = typ.Usage()
	}
	f := r.Frame
	f.Enter(loop)
	defer f.Leave(loop)
	for {
		// An empty body and a continue-only body must both remain cancellable.
		if err := r.Host.Context().Err(); err != nil {
			return sqltype.Value{}, PLFlow{}, err
		}
		if in.While != nil {
			raw := in.Syntax
			at := PLOrigin{Position: raw.Pos, Action: "at WHILE"}
			value, _, err := r.Host.Evaluate(f, in.While.Condition, at)
			expressionError := err != nil
			if err == nil {
				value, err = r.Host.Convert(value, boolean, at)
			}
			if err != nil {
				condition := raw.While.Condition
				return sqltype.Value{}, PLFlow{}, PLRuntimeError(err, r.Definition, r.Host.Identity, condition.Start, condition.End, raw.Pos, at.Action, expressionError)
			}
			if value.IsNull() {
				return sqltype.Value{}, PLFlow{}, nil
			}
			truth, err := value.Boolean()
			if err != nil {
				return sqltype.Value{}, PLFlow{}, err
			}
			if !truth {
				return sqltype.Value{}, PLFlow{}, nil
			}
		}
		value, flow, err := r.Statements(loop.Statements)
		if err != nil || flow.Kind == PLReturn {
			return value, flow, err
		}
		if flow.Kind == PLNormal {
			continue
		}
		if flow.Target != loop.Control {
			return value, flow, nil
		}
		if flow.Kind == PLExit {
			return sqltype.Value{}, PLFlow{}, nil
		}
		if flow.Kind != PLContinue {
			return sqltype.Value{}, PLFlow{}, fmt.Errorf("stuntgres: invalid PL loop flow")
		}
	}
}

func (r *PLRun) transfer(in PLInstruction) (sqltype.Value, PLFlow, error) {
	raw := in.Syntax
	action, kind := "at EXIT", PLExit
	if raw.Transfer.Continue {
		action, kind = "at CONTINUE", PLContinue
	}
	if in.Query != nil {
		at := PLOrigin{Position: raw.Pos, Action: action}
		value, _, err := r.Host.Evaluate(r.Frame, in.Query, at)
		expressionError := err != nil
		if err == nil {
			boolean, ok := r.Host.LookupType(sqltype.Bool)
			if !ok {
				return sqltype.Value{}, PLFlow{}, fmt.Errorf("stuntgres: missing PL condition Boolean type")
			}
			value, err = r.Host.Convert(value, boolean.Usage(), at)
		}
		if err != nil {
			return sqltype.Value{}, PLFlow{}, PLRuntimeError(err, r.Definition, r.Host.Identity, raw.ExpressionStart, raw.ExpressionEnd, raw.Pos, action, expressionError)
		}
		if value.IsNull() {
			return sqltype.Value{}, PLFlow{}, nil
		}
		truth, err := value.Boolean()
		if err != nil {
			return sqltype.Value{}, PLFlow{}, err
		}
		if !truth {
			return sqltype.Value{}, PLFlow{}, nil
		}
	}
	return sqltype.Value{}, PLFlow{Kind: kind, Target: in.Transfer}, nil
}

func (r *PLRun) conditional(instruction PLInstruction) (sqltype.Value, PLFlow, error) {
	f := r.Frame
	checked, raw, action := instruction.If, instruction.Syntax.If, "at IF"
	isCase := instruction.Case != nil
	if isCase {
		checked, raw, action = instruction.Case, instruction.Syntax.Case, "at CASE"
	}
	at := PLOrigin{Position: instruction.Syntax.Pos, Action: action}
	if checked.Search != nil {
		if err := r.Host.Context().Err(); err != nil {
			return sqltype.Value{}, PLFlow{}, err
		}
		value, usage, err := r.Host.Evaluate(f, checked.Search, at)
		if err != nil {
			return sqltype.Value{}, PLFlow{}, PLRuntimeError(err, r.Definition, r.Host.Identity, raw.Search.Start, raw.Search.End, instruction.Syntax.Pos, action, true)
		}
		f.Searches[checked.SearchSlot] = PLSearch{Value: value, Usage: usage}
		defer func() { f.Searches[checked.SearchSlot] = PLSearch{} }()
	}
	boolean, ok := r.Host.LookupType(sqltype.Bool)
	if !ok {
		return sqltype.Value{}, PLFlow{}, errors.New("stuntgres: missing PL condition Boolean type")
	}
	for i, branch := range checked.Arms {
		if err := r.Host.Context().Err(); err != nil {
			return sqltype.Value{}, PLFlow{}, err
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
				err = PLCaseExpressionError(err, branch, first.Start)
			}
			return sqltype.Value{}, PLFlow{}, PLRuntimeError(err, r.Definition, r.Host.Identity, first.Start, arm.Conditions[len(arm.Conditions)-1].End, instruction.Syntax.Pos, action, expressionError && checked.Search == nil)
		}
		if value.IsNull() {
			continue
		}
		truth, err := value.Boolean()
		if err != nil {
			return sqltype.Value{}, PLFlow{}, err
		}
		if truth {
			return r.Statements(branch.Statements)
		}
	}
	if isCase && !checked.HasElse {
		err := plError("20000", "case not found")
		err.Hint = "CASE statement is missing ELSE part."
		return sqltype.Value{}, PLFlow{}, PLRuntimeError(err, r.Definition, r.Host.Identity, parser.Position{}, parser.Position{}, instruction.Syntax.Pos, action, false)
	}
	return r.Statements(checked.Else)
}
