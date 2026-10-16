package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

// Entries are immutable once published by Execute under Engine.mu. A candidate
// borrows the old group and copies only when a successful fold changes it.
type quoteOrigin struct {
	owner      ObjectRef
	attribute  int32
	definition OID
	revision   DefinitionRevision
}
type quoteConstant struct {
	offset     int
	origin     quoteOrigin
	frame      uint32
	name       string
	anyelement bool
	source     sqltype.Usage
	result     sqltype.Usage
	value      sqltype.Value
}
type quoteCaptureCandidate struct {
	entries []quoteConstant
	private bool
	sealed  bool
}

func foldStringVariadic(c *EvalContext, e functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin]) (BoundExpr, bool, error) {
	p := e.Properties()
	if e.IntrinsicStable() || p.Volatility != Immutable || !conditionalConstantInputs(e) {
		return e, false, nil
	}
	if err := admitRoutineCall(c, e, true, true); err != nil {
		return nil, false, err
	}
	retained := c.quoteCaptures != nil && !p.RowDependent && !p.ParameterDependent && !p.StageDependent
	var entry quoteConstant
	index := -1
	if retained {
		origin := e.Origin
		if origin.owner == (ObjectRef{}) {
			origin = c.quoteOrigin
		}
		entry = quoteConstant{offset: e.Pos.Offset, origin: origin, frame: e.Frame, name: e.Name, anyelement: e.AnyElement, source: e.Args[0].ResultUsage(), result: e.Usage}
		for i, saved := range c.quoteCaptures.entries {
			if saved.offset != entry.offset || saved.origin != entry.origin || saved.frame != entry.frame || saved.name != entry.name || saved.anyelement != entry.anyelement {
				continue
			}
			index = i
			if saved.source == entry.source && saved.result == entry.result {
				return literalExpression{foldedExpressionInfo(e), saved.value}, true, nil
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
	return literalExpression{foldedExpressionInfo(e), value}, true, nil
}

// Definition tokens, not unrelated catalog revisions, retire stored origins.
// The stable case borrows the entire published group without allocating.
func (g *quoteCaptureCandidate) prune(view CatalogRead) {
	valid := func(origin quoteOrigin) bool {
		if origin.owner == (ObjectRef{}) {
			return true
		}
		if view.owner == nil || origin.owner.owner != view.owner.owner || origin.owner.Epoch != view.Epoch() {
			return false
		}
		d, ok := view.state.descriptors[origin.owner.Address].(*relationDescriptor)
		if !ok {
			return false
		}
		if origin.attribute != 0 {
			return d.defaults[origin.attribute].OID == origin.definition
		}
		return d.view != nil && d.view.rule == origin.definition && d.view.quoteRevision == origin.revision
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
