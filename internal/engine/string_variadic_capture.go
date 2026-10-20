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
	retained := c.quoteCaptures != nil && !p.RowDependent && !p.ParameterDependent && !p.StageDependent
	var entry quoteConstant
	index := -1
	if retained {
		origin := quoteOriginOf(e)
		if origin.owner == (ids.ObjectRef{}) {
			origin = c.quoteOrigin
		}
		entry = quoteConstant{offset: e.Pos.Offset, origin: origin, frame: e.Frame, name: e.Name, anyelement: e.AnyElement, source: e.Args[0].ResultUsage(), result: e.Usage}
		for i, saved := range c.quoteCaptures.entries {
			if saved.offset != entry.offset || saved.origin != entry.origin || saved.frame != entry.frame || saved.name != entry.name || saved.anyelement != entry.anyelement {
				continue
			}
			index = i
			if saved.source == entry.source && saved.result == entry.result {
				return plan.LiteralExpr{Info: foldedExpressionInfo(e), Value: saved.value}, true, nil
			}
			break
		}
	}
	value, err := e.Eval(c)
	if err != nil {
		return nil, false, err
	}
	if retained && !c.quoteCaptures.sealed {
		entry.value = value
		group := c.quoteCaptures
		if !group.private {
			group.entries = append([]quoteConstant(nil), group.entries...)
			group.private = true
		}
		if index >= 0 {
			group.entries[index] = entry
		} else {
			group.entries = append(group.entries, entry)
		}
	}
	return plan.LiteralExpr{Info: foldedExpressionInfo(e), Value: value}, true, nil
}

// Definition tokens, not unrelated catalog revisions, retire stored origins.
// The stable case borrows the entire published group without allocating.
func pruneQuoteCaptures(g *quoteCaptureCandidate, view catalog.Read) {
	valid := func(origin quoteOrigin) bool {
		if origin.owner == (ids.ObjectRef{}) {
			return true
		}
		if view.Owner() == nil || origin.owner.Domain() != view.Owner().Domain || origin.owner.Epoch != view.Epoch() {
			return false
		}
		d, ok := view.Relation(origin.owner.Address)
		if !ok {
			return false
		}
		if origin.attribute != 0 {
			return d.ColumnMeta.Defaults[origin.attribute].OID == origin.definition
		}
		return d.View != nil && viewOf(d).rule == origin.definition && viewOf(d).quoteRevision == origin.revision
	}
	for i, entry := range g.entries {
		if valid(entry.origin) {
			continue
		}
		next := make([]quoteConstant, 0, len(g.entries)-1)
		next = append(next, g.entries[:i]...)
		for _, remaining := range g.entries[i+1:] {
			if valid(remaining.origin) {
				next = append(next, remaining)
			}
		}
		g.entries, g.private = next, true
		return
	}
}
