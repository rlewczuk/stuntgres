package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
)

// Released IO rules select a destination-compatible value, not a universal
// 'red'. Ordinary target seeds fit NAME's 63-byte input bound. Logical source
// domain constraints are not inferred from the source's physical base identity.
func castStringMatrixValue(cell castMatrixCell) castMatrixValue {
	source, target := cell.source, cell.target
	value := castMatrixValue{input: source.seed, output: source.output}
	if !cell.contexts[2].admitted {
		return value
	}
	if value, constrained := castConstrainedStringValue(cell); constrained {
		return value
	}
	if target.base != "" {
		// Logical target seeds satisfy its own CHECK/NOT NULL and sizing recipe.
		value.input, value.output = target.seed, target.output
		return value
	}
	if edge := cell.contexts[2].edge; edge != nil {
		witness := castDirectWitness(*edge)
		value.input, value.output = witness.input, witness.output
		return value
	}
	value.input, value.output = target.seed, target.output
	switch target.name {
	case "void":
		// REL_18_6 pseudotypes.c void_in ignores arbitrary non-NULL input.
		value.input, value.output = source.seed, ""
	case "pg_node_tree":
		// This is a legal type path with an intentionally refusing real input
		// function. Its typed NULL result is checked separately, not forbidden.
		value.input = source.seed
		value.diagnostic = &fixture.Diagnostic{Code: "0A000", Message: "cannot accept a value of type pg_node_tree"}
	case "cast_anonymous_record", "cast_deferred_record", "record[]":
		// REL_18_6 rowtypes.c record_in needs an actual tuple descriptor. RECORD
		// and its generic array cannot acquire one by importing an arbitrary string.
		value.diagnostic = &fixture.Diagnostic{Code: "0A000", Message: "input of anonymous composite types is not implemented"}
	}
	return value
}

func castFunctionTargetBoundary(cell castMatrixCell, query string, postgres bool) *fixture.Diagnostic {
	target, decision := cell.target, cell.contexts[2]
	if postgres || !decision.admitted || decision.operation != "typename-shortcut" || castInputFreeTarget(cell) {
		return nil
	}
	unavailable := target.name == "void" || target.name == "cast_anonymous_record" || target.name == "cast_deferred_record" || target.name == "record[]"
	if target.element != "" && castInventoryType(target.element).kind == "e" {
		unavailable = true
	}
	if !unavailable {
		return nil
	}
	// sql_routine_resolution builds TypeName.Pos from the actual call's Pos,
	// which grammar.y retains at the first qualified name token. These are the
	// existing resolver-owned generic-input boundaries, not forbidden pairs.
	position := strings.Index(query, castFunctionName(target)) + 1
	return &fixture.Diagnostic{Code: "0A000", Message: "unsupported column type", Position: fixture.Position(position)}
}
