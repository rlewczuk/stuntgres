package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// quoteOriginOf reads the opaque occurrence origin the engine stored on e. An
// unset origin is the zero quoteOrigin.
func quoteOriginOf(e functions.StringVariadic) quoteOrigin {
	origin, _ := e.Origin.(quoteOrigin)
	return origin
}

func foldStringVariadic(c *EvalContext, e functions.StringVariadic) (expr.Expr, bool, error) {
	p := e.Properties()
	if e.IntrinsicStable() || p.Volatility != expr.Immutable || !conditionalConstantInputs(e) {
		return e, false, nil
	}
	if err := admitRoutineCall(c, e, true, true); err != nil {
		return nil, false, err
	}
	retained := c.QuoteCaptures != nil && !p.RowDependent && !p.ParameterDependent && !p.StageDependent
	var entry quoteConstant
	index := -1
	if retained {
		origin := quoteOriginOf(e)
		if origin.Owner == (ids.ObjectRef{}) {
			origin = c.QuoteOrigin
		}
		entry = quoteConstant{Offset: e.Pos.Offset, Origin: origin, Frame: e.Frame, Name: e.Name, Anyelement: e.AnyElement, Source: e.Args[0].ResultUsage(), Result: e.Usage}
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
			group.Entries = append([]quoteConstant(nil), group.Entries...)
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

// Definition tokens, not unrelated catalog revisions, retire stored origins.
// The stable case borrows the entire published group without allocating.
func pruneQuoteCaptures(g *quoteCaptureCandidate, view catalog.Read) {
	valid := func(origin quoteOrigin) bool {
		if origin.Owner == (ids.ObjectRef{}) {
			return true
		}
		if view.Owner() == nil || origin.Owner.Domain() != view.Owner().Domain || origin.Owner.Epoch != view.Epoch() {
			return false
		}
		d, ok := view.Relation(origin.Owner.Address)
		if !ok {
			return false
		}
		if origin.Attribute != 0 {
			return d.ColumnMeta.Defaults[origin.Attribute].OID == origin.Definition
		}
		return d.View != nil && viewOf(d).rule == origin.Definition && viewOf(d).quoteRevision == origin.Revision
	}
	for i, entry := range g.Entries {
		if valid(entry.Origin) {
			continue
		}
		next := make([]quoteConstant, 0, len(g.Entries)-1)
		next = append(next, g.Entries[:i]...)
		for _, remaining := range g.Entries[i+1:] {
			if valid(remaining.Origin) {
				next = append(next, remaining)
			}
		}
		g.Entries, g.Private = next, true
		return
	}
}
