package dml

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

// All RHS expressions read the unchanged action frame. Partial assignments
// compose only in the replacement, never in the frame seen by the next RHS.
func EvaluateUpdateAssignments(eval *exec.Context, action *analyze.UpdateBinding, targets []int, old []sqltype.Value) ([]sqltype.Value, error) {
	row := make([]sqltype.Value, len(old))
	copy(row, old)
	oldOutputs, oldReady := eval.UpdateOutputs, eval.UpdateOutputsReady
	if action.OutputWidth != 0 {
		if oldOutputs != nil {
			// A nested mutation owner must not overwrite the caller's live tuple.
			previousScratch := eval.UpdateOutputScratch
			var small [4]sqltype.Value
			if action.OutputWidth <= len(small) {
				eval.UpdateOutputScratch = small[:action.OutputWidth]
			} else {
				eval.UpdateOutputScratch = make([]sqltype.Value, action.OutputWidth)
			}
			defer func() { eval.UpdateOutputScratch = previousScratch }()
		}
		if cap(eval.UpdateOutputScratch) < action.OutputWidth {
			eval.UpdateOutputScratch = make([]sqltype.Value, action.OutputWidth)
		}
		defer func() { eval.UpdateOutputs, eval.UpdateOutputsReady = oldOutputs, oldReady }()
	}
	group := 0
	for i, expr := range action.Assignments {
		if group < len(action.Groups) && action.Groups[group].Start == i {
			eval.UpdateOutputs = eval.UpdateOutputScratch[:action.Groups[group].Width]
			eval.UpdateOutputsReady = false
			group++
		}
		if expr == nil {
			continue
		}
		target := targets[i]
		var value sqltype.Value
		var err error
		if len(action.ArrayTargets) != 0 && action.ArrayTargets[i] != nil {
			value, err = action.ArrayTargets[i].ApplyExpression(eval, row[target], expr)
		} else if len(action.DefaultOrigins) != 0 && action.DefaultOrigins[i].Requested {
			value, err = analyze.EvalDefault(eval, expr, action.DefaultOrigins[i])
		} else {
			value, err = expr.Eval(eval)
		}
		if err != nil {
			return nil, err
		}
		row[target] = value
	}
	return row, nil
}
