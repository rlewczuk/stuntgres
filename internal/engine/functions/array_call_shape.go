package functions

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// arrayInputTarget is the shared anyarray input rule. An unknown remains
// unresolved until a selected provider supplies its polymorphic diagnostic.
func arrayInputTarget(u sqltype.Usage, catalogNull bool) (sqltype.Usage, bool) {
	if u.Type == nil || catalogNull && u.CatalogNullUsage() {
		return u, true
	}
	if !u.Type.BaseType().IsArray() {
		return sqltype.Usage{}, false
	}
	return u.Type.BaseType().Usage(), true
}

func probeArrayInput(b CallTypeContext, raw parser.FunctionExpr, operands []Argument, declared, targets []sqltype.Usage) bool {
	var ok bool
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	targets[0], ok = arrayInputTarget(operands[0].Usage, ArrayRandomFunction(name) == 0)
	return ok && knownCallEdges(b, operands, targets)
}

func probeArrayTransform(b CallTypeContext, raw parser.FunctionExpr, operands []Argument, declared, targets []sqltype.Usage) bool {
	kind := ArrayTransformFunction(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	if kind == ArrayAppend || kind == ArrayPrepend || kind == ArrayConcatenate {
		arrays := [2]bool{kind != ArrayPrepend, kind != ArrayAppend}
		_, ok := compatibleArrayTargets(b, operands, arrays[:], targets, true)
		return ok
	}
	if kind == ArrayFill {
		targets[0] = operands[0].Usage
		return knownCallEdges(b, operands, targets)
	}
	return probeArrayInput(b, raw, operands, declared, targets)
}

func probeArraySearch(b CallTypeContext, raw parser.FunctionExpr, operands []Argument, declared, targets []sqltype.Usage) bool {
	n := len(operands)
	if raw.Name.Parts[len(raw.Name.Parts)-1].Text == "array_position" && n == 3 {
		n = 2
	}
	arrays := [3]bool{true, false, false}
	_, ok := compatibleArrayTargets(b, operands[:n], arrays[:n], targets[:n], false)
	return ok && knownCallEdges(b, operands, targets)
}

func probeThresholdBucket(b CallTypeContext, raw parser.FunctionExpr, operands []Argument, declared, targets []sqltype.Usage) bool {
	if len(operands) != 2 {
		return false
	}
	arrays := [2]bool{false, true}
	_, ok := compatibleArrayTargets(b, operands, arrays[:], targets, false)
	return ok
}

func arrayInputCallResult(_ CallTypeContext, _ parser.FunctionExpr, targets []sqltype.Usage) (sqltype.Usage, bool) {
	return targets[0], true
}
