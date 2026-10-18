package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func windowValueName(name string) bool {
	return name == "first_value" || name == "last_value" || name == "nth_value"
}

func (b *expressionBinder) resolveWindowValueSignature(raw parser.AggregateExpr, name parser.ObjectName) (value, ordinal BoundExpr, usage sqltype.Usage, err error) {
	var small [2]functions.Argument
	var names [2]string
	arguments, types := small[:], names[:]
	if len(raw.Args) <= 2 {
		arguments, types = arguments[:len(raw.Args)], types[:len(raw.Args)]
	} else {
		arguments = make([]functions.Argument, len(raw.Args))
		types = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arguments[i], err = b.AnalyzeArgument(child, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return
		}
		types[i] = "unknown"
		if typ := arguments[i].Usage.Type; typ != nil {
			types[i] = b.diagnosticTypeName(typ)
		}
	}
	if _, err = b.bindAggregateFilter(raw); err != nil {
		return
	}
	if len(raw.Args) > 100 {
		err = expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
		return
	}
	var visible bool
	visible, err = b.builtinVisible(name, functionLookup)
	if err != nil {
		return
	}
	var valueUsage, ordinalUsage sqltype.Usage
	if len(arguments) > 0 {
		valueUsage = arguments[0].Usage
	}
	if len(arguments) > 1 {
		ordinalUsage = arguments[1].Usage
	}
	integer := b.typ(sqltype.Int4).Usage()
	decision, plan := functions.ResolveWindowValue(visible, raw.Star, raw.Name == "nth_value", len(arguments), valueUsage, ordinalUsage, integer)
	switch decision {
	case functions.Missing:
		err = functionSignatureErrorForTypes(name, raw.Pos, types)
		return
	case functions.UnknownPolymorphic:
		err = sqlError("42804", "could not determine polymorphic type because input has type unknown")
		return
	}
	value = arguments[0].Expression
	if raw.Name == "nth_value" {
		ordinal, err = b.CoerceArgument(arguments[1], functions.CoercionRequest{Intent: functions.ExactFormalArgument, Target: integer, AtRaw: true, Plan: plan, Planned: ordinalUsage.Type != nil})
		if err != nil {
			return
		}
	}
	usage = value.ResultUsage()
	usage.Typmod = -1
	return
}

// A sort-input boundary retains exact required targets before matching window
// leaves from postponed volatile payloads. Only an ACTUAL whole retained target
// can own a leaf; unmatched postponed leaves keep independent execution slots.
func (b *expressionBinder) windowValueAliases(calls []windowCall, projection []BoundExpr, order, on []sortKey) []int {
	if len(order) == 0 {
		return nil
	}
	hasValues := false
	for _, call := range calls {
		hasValues = hasValues || call.operation.evaluatesArgument()
	}
	if !hasValues {
		return nil
	}
	demanded := func(output int) bool {
		for _, keys := range [2][]sortKey{order, on} {
			for _, key := range keys {
				if key.outputOrdinal == output+1 || key.targetIdentity == output+1 {
					return true
				}
			}
		}
		return false
	}
	owners := make([]*windowExpression, 0, len(calls))
	retain := func(expression BoundExpr) {
		leaf, ok := expression.(*windowExpression)
		if !ok || leaf.frame != b.frameID() || leaf.call < 0 || leaf.call >= len(calls) || !calls[leaf.call].operation.evaluatesArgument() {
			return
		}
		for _, owner := range owners {
			if owner.call == leaf.call {
				return
			}
		}
		owners = append(owners, leaf)
	}
	for i, expression := range projection {
		if demanded(i) || expression.Properties().Volatility != Volatile {
			retain(expression)
		}
	}
	for _, keys := range [2][]sortKey{order, on} {
		for _, key := range keys {
			retain(key.expression)
		}
	}
	var aliases []int
	for i, expression := range projection {
		if demanded(i) || expression.Properties().Volatility != Volatile {
			continue
		}
		walkBoundExpression(expression, func(child BoundExpr) {
			leaf, ok := child.(*windowExpression)
			if !ok || leaf.frame != b.frameID() || leaf.call < 0 || leaf.call >= len(calls) || !calls[leaf.call].operation.evaluatesArgument() {
				return
			}
			for _, owner := range owners {
				if !b.sameWindowCall(calls[leaf.call], calls[owner.call]) {
					continue
				}
				if leaf.call != owner.call {
					if aliases == nil {
						aliases = make([]int, len(calls))
						for j := range aliases {
							aliases[j] = -1
						}
					}
					aliases[leaf.call] = owner.call
				}
				return
			}
		})
	}
	return aliases
}
