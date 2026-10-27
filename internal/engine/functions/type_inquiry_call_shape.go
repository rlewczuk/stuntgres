package functions

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Probe only selects the existing declaration. In particular, ANY retains the
// operand's logical usage, including an unresolved parameter or UNKNOWN; it
// neither coerces transport nor supplies TEXT for overload ranking.
func probeTypeInquiry(b CallTypeContext, raw parser.FunctionExpr, operands []Argument, declared, targets []sqltype.Usage) bool {
	if len(raw.Name.Parts) == 0 || len(raw.ArgNames) != 0 {
		return false
	}
	formals := [2]sqltype.ID{2276, 0}
	arity := 1
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	switch name {
	case "pg_typeof":
	case "pg_basetype":
		formals[0] = sqltype.Regtype
	case "format_type":
		arity = 2
		formals = [2]sqltype.ID{sqltype.Oid, sqltype.Int4}
	default:
		return false
	}
	if len(operands) != arity || len(declared) != arity || len(targets) != arity {
		return false
	}
	for i, formal := range declared {
		if formal.Type == nil || formal.Type.ID() != formals[i] {
			return false
		}
	}
	if name == "pg_typeof" {
		retainAnyCallTargets(operands, declared, targets)
		return true
	}
	if !knownCallEdges(b, operands, declared) {
		return false
	}
	copy(targets, declared)
	return true
}
