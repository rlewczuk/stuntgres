package functions

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Any formals retain their operand types; they do not import unknowns as TEXT
// or claim an exact concrete signature for ranking.
func retainAnyCallTargets(operands []Argument, declared, targets []sqltype.Usage) {
	for i, input := range declared {
		if !input.Type.Concrete() {
			targets[i] = operands[i].Usage
		}
	}
}

func probeFormat(b CallTypeContext, raw parser.FunctionExpr, operands []Argument, declared, targets []sqltype.Usage) bool {
	if len(operands) == 0 || ResolveFormat(true, raw.Variadic, len(operands), operands[0].Usage) != Resolved {
		return false
	}
	retainAnyCallTargets(operands, declared, targets)
	return true
}

func probeStringVariadic(b CallTypeContext, raw parser.FunctionExpr, operands []Argument, declared, targets []sqltype.Usage) bool {
	var first sqltype.Usage
	if len(operands) != 0 {
		first = operands[0].Usage
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if ResolveStringVariadic(name, true, raw.Variadic, raw.ResolutionOnly, len(raw.ArgNames), len(operands), first) != Resolved {
		return false
	}
	retainAnyCallTargets(operands, declared, targets)
	return true
}

func rangeCallFormal(selected *sqltype.Descriptor) sqltype.ID {
	switch selected.ID() {
	case sqltype.Int4Multirange, sqltype.Int8Multirange, sqltype.NumMultirange:
		return 4537
	default:
		return 3831
	}
}

func probeRangeInspect(b CallTypeContext, raw parser.FunctionExpr, operands []Argument, declared, targets []sqltype.Usage) bool {
	operation, known := sqltype.RangeInspectKind(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	selected, resolution := ResolveRangeInspect(operation, known, operands)
	if resolution == Missing {
		return false
	}
	if selected == nil {
		for i := range targets {
			targets[i] = sqltype.Usage{}
		}
		return true
	}
	if declared[0].Type.ID() != rangeCallFormal(selected) {
		return false
	}
	for i := range targets {
		targets[i] = selected.Usage()
	}
	return true
}

func probeJSONCall(b CallTypeContext, raw parser.FunctionExpr, operands []Argument, declared, targets []sqltype.Usage) bool {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if kind := JSONCreateKind(name); kind != 0 {
		if ResolveJSONCreate(kind, name, true, raw.Variadic, raw.ResolutionOnly, len(raw.ArgNames), operands, b.Type(sqltype.Bool).Usage()) != Resolved {
			return false
		}
		retainAnyCallTargets(operands, declared, targets)
		return true
	}
	return probeJSONRecordCall(b, raw, operands, declared, targets)
}

func probeJSONRecordCall(b CallTypeContext, raw parser.FunctionExpr, operands []Argument, declared, targets []sqltype.Usage) bool {
	retainAnyCallTargets(operands, declared, targets)
	for i, input := range declared {
		if input.Type.ID() == sqltype.JSON || input.Type.ID() == sqltype.JSONB {
			if ResolveJSONArgument(operands[i].Usage, input) != Resolved {
				return false
			}
		}
	}
	return true
}

func rangeInspectCallResult(_ CallTypeContext, raw parser.FunctionExpr, targets []sqltype.Usage) (sqltype.Usage, bool) {
	if targets[0].Type == nil {
		return sqltype.Usage{}, true
	}
	operation, known := sqltype.RangeInspectKind(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	if !known {
		return sqltype.Usage{}, false
	}
	result := sqltype.RangeInspectResult(operation, targets[0].Type)
	if result == nil {
		return sqltype.Usage{}, false
	}
	return result.Usage(), true
}

func jsonRecordCallResult(_ CallTypeContext, _ parser.FunctionExpr, targets []sqltype.Usage) (sqltype.Usage, bool) {
	return targets[0], true
}
