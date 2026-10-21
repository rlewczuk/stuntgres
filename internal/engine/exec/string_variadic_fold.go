package exec

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// QuoteOriginOf reads the opaque occurrence origin the engine stored on e. An
// unset origin is the zero QuoteOrigin.
func QuoteOriginOf(e functions.StringVariadic) QuoteOrigin {
	origin, _ := e.Origin.(QuoteOrigin)
	return origin
}

// FoldStringVariadic folds an immutable variadic string call with constant
// inputs after routine admission, reusing or recording the retained quote
// capture of its occurrence.
func FoldStringVariadic(c *Context, e functions.StringVariadic) (expr.Expr, bool, error) {
	p := e.Properties()
	if e.IntrinsicStable() || p.Volatility != expr.Immutable || !ConditionalConstantInputs(e) {
		return e, false, nil
	}
	if err := AdmitRoutineCall(c, e, true, true); err != nil {
		return nil, false, err
	}
	retained := c.QuoteCaptures != nil && !p.RowDependent && !p.ParameterDependent && !p.StageDependent
	var entry QuoteConstant
	index := -1
	if retained {
		origin := QuoteOriginOf(e)
		if origin.Owner == (ids.ObjectRef{}) {
			origin = c.QuoteOrigin
		}
		entry = QuoteConstant{Offset: e.Pos.Offset, Origin: origin, Frame: e.Frame, Name: e.Name, Anyelement: e.AnyElement, Source: e.Args[0].ResultUsage(), Result: e.Usage}
		for i, saved := range c.QuoteCaptures.Entries {
			if saved.Offset != entry.Offset || saved.Origin != entry.Origin || saved.Frame != entry.Frame || saved.Name != entry.Name || saved.Anyelement != entry.Anyelement {
				continue
			}
			index = i
			if saved.Source == entry.Source && saved.Result == entry.Result {
				return plan.LiteralExpr{Info: foldedExpressionInfo(e), Value: saved.Value}, true, nil
			}
			break
		}
	}
	value, err := e.Eval(c)
	if err != nil {
		return nil, false, err
	}
	if retained && !c.QuoteCaptures.Sealed {
		entry.Value = value
		group := c.QuoteCaptures
		if !group.Private {
			group.Entries = append([]QuoteConstant(nil), group.Entries...)
			group.Private = true
		}
		if index >= 0 {
			group.Entries[index] = entry
		} else {
			group.Entries = append(group.Entries, entry)
		}
	}
	return plan.LiteralExpr{Info: foldedExpressionInfo(e), Value: value}, true, nil
}
