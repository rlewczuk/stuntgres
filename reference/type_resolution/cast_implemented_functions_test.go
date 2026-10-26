package reference_test

import (
	"strings"

	"stuntgres/reference/fixture"
)

type castReleasedCallable struct{ callee, argument, result, authority string }

// These are released pg_proc.dat one-argument conversion signatures, not an
// assertion that every CAST is a function call. A cast naming text(bool) does
// not manufacture varchar(bool), and bit(int4,int4) has no one-argument form.
// Additional released constructors not represented by pg_cast are explicit.
var castReleasedCallables = func() []castReleasedCallable {
	var signatures []castReleasedCallable
	seen := map[string]bool{}
	add := func(callee, argument, result, authority string) {
		key := callee + "(" + argument + ")"
		if seen[key] {
			return
		}
		seen[key] = true
		signatures = append(signatures, castReleasedCallable{callee, argument, result, authority})
	}
	for _, edge := range castReleasedEdges {
		name, args, ok := strings.Cut(edge.function, "(")
		if !ok || strings.Contains(args, ",") {
			continue
		}
		if strings.Trim(name, `"`) != strings.Trim(edge.target, `"`) {
			continue
		}
		argument := strings.TrimSuffix(args, ")")
		add(edge.target, argument, edge.target, "REL_18_6 pg_proc.dat conversion signature")
	}
	add("cube", "float8", "cube", "REL_18_6 contrib/cube/cube--1.2.sql cube(float8)")
	add("cube", "float8[]", "cube", "REL_18_6 contrib/cube/cube--1.2.sql cube(float8[])")
	return signatures
}()

type castFunctionDecision struct {
	admitted             bool
	operation, authority string
	callable             *castReleasedCallable
	ambiguous            bool
}

func castScalarFunctionPath(cell castMatrixCell) castFunctionDecision {
	source, target := cell.source, cell.target
	// Exact callable selection precedes the typename shortcut.
	for i := range castReleasedCallables {
		fn := &castReleasedCallables[i]
		if fn.callee == target.name && fn.argument == source.name {
			return castFunctionDecision{true, "exact-callable", fn.authority, fn, false}
		}
	}
	path := cell.contexts[2]
	shortcut := path.admitted && target.kind != "c" && (path.operation == "identity/typmod" || path.operation == "domain-base" || path.operation == "io" || path.operation == "direct" && path.edge != nil && (path.edge.method == 'b' || path.edge.method == 'i'))
	if path.operation == "io" && (source.kind == "c" || source.oid == 2249) && target.category == "S" {
		shortcut = false
	}
	if shortcut {
		return castFunctionDecision{true, "typename-shortcut", "REL_18_6 parse_func.c func_get_detail IO/relabel", nil, false}
	}
	var candidates []*castReleasedCallable
	for i := range castReleasedCallables {
		fn := &castReleasedCallables[i]
		if fn.callee != target.name {
			continue
		}
		argument := castInventoryType(fn.argument)
		if castReleasedPath(source, argument, 'i').admitted {
			candidates = append(candidates, fn)
		}
	}
	// After the initial exact logical-type pass, PG best-match treats domains
	// as their physical base before counting exact argument matches.
	if source.base != "" {
		for _, fn := range candidates {
			if fn.argument == castPhysicalFact(source).name {
				return castFunctionDecision{true, "implicit-callable", "PG18 typeconv-func domain base exact argument", fn, false}
			}
		}
	}
	if len(candidates) > 1 {
		// Preferred identities are independent pg_type.dat facts. Closeness in
		// a cast graph never chooses a callable; same-category preferred does.
		var preferred []*castReleasedCallable
		preferredType := map[string]string{"B": "bool", "N": "float8", "D": "timestamptz", "T": "interval", "S": "text", "V": "varbit", "I": "inet"}[source.category]
		for _, fn := range candidates {
			if fn.argument == preferredType {
				preferred = append(preferred, fn)
			}
		}
		if len(preferred) > 0 {
			candidates = preferred
		}
	}
	if len(candidates) == 1 {
		return castFunctionDecision{true, "implicit-callable", "PG18 typeconv-func best-match implicit-only arguments", candidates[0], false}
	}
	if len(candidates) > 1 {
		return castFunctionDecision{false, "ambiguous", "PG18 typeconv-func unresolved best-match candidates", nil, true}
	}
	return castFunctionDecision{false, "missing", "PG18 typeconv-func no exact/shortcut/implicit candidate", nil, false}
}

func castScalarFunctionCell(cell castMatrixCell) (castMatrixCell, castMatrixValue, *fixture.Diagnostic) {
	decision := castScalarFunctionPath(cell)
	value := castMatrixValue{input: cell.source.seed, output: cell.source.output}
	original := cell
	cell.contexts[2] = castReleasedDecision{admitted: decision.admitted, operation: decision.operation, authority: decision.authority}
	if !decision.admitted {
		name := strings.ReplaceAll(castFunctionName(cell.target), `"`, "")
		diagnostic := &fixture.Diagnostic{Code: "42883", Message: "function " + name + "(" + cell.source.diagnostic + ") does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}
		if decision.ambiguous {
			diagnostic.Code = "42725"
			diagnostic.Message = "function " + name + "(" + cell.source.diagnostic + ") is not unique"
			diagnostic.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
		}
		return cell, value, diagnostic
	}
	if decision.callable == nil {
		return cell, castScalarMatrixValue(original), nil
	}
	if decision.callable.callee == "cube" {
		value.input = "1"
		if decision.callable.argument == "float8[]" {
			value.input = "{1}"
		}
		value.output = "(1)"
		return cell, value, nil
	}
	// For an implicit adapter the selected real routine owns the output. Its
	// independent witness is applied to a valid source value (e.g. int2->int4
	// adapter then bool(int4)), not to an invented transitive CAST pathway.
	witness := castDirectWitness(castReleasedEdge{source: decision.callable.argument, target: decision.callable.result})
	value.input, value.output = witness.input, witness.output
	value.polygon = decision.callable.argument == "circle" && decision.callable.result == "polygon"
	if cell.source.name == `"char"` && decision.callable.callee == "regclass" {
		// The CHAR source truncates the TEXT routine's long input seed to 'r';
		// its implicit char_text adapter therefore passes "r" to regclass.
		value.input, value.output = "r", "r"
	}
	if decision.callable.argument != cell.source.name && cell.source.family == "Temporal" {
		// Do not import the adapter target's timestamp spelling as a DATE or
		// TIME source. The literal epoch source has the same independently
		// expected UTC result under the scenario's captured UTC environment.
		value.input = cell.source.seed
	}
	if constrained, ok := castConstrainedStringValue(original); ok {
		value = constrained
	}
	value = castConstrainedNumericValue(cell.source, value)
	return cell, value, nil
}

// parse_func.c145-164 removes VOID Param nodes (not VOID-valued expressions)
// before lookup. Resolve the resulting zero-argument call independently.
func castProtocolFunctionCell(cell castMatrixCell) (castMatrixCell, castMatrixValue, *fixture.Diagnostic) {
	if cell.source.name != "void" {
		return castScalarFunctionCell(cell)
	}
	value := castMatrixValue{input: "ignored"}
	cell.contexts[2] = castReleasedDecision{false, "void-param-elision", "REL_18_6 parse_func.c145-164 JDBC Param normalization", nil}
	name := strings.ReplaceAll(castFunctionName(cell.target), `"`, "")
	if cell.target.kind == "m" || cell.target.name == "earth" {
		// pg_proc.dat multirange_constructor0/typecmds.c makeMultirangeConstructors;
		// earthdistance--1.1.sql earth() RETURNS float8, not the earth domain.
		cell.contexts[2].admitted = true
		cell.contexts[2].operation = "void-param-zero-callable"
		value.output = "{}"
		if cell.target.name == "earth" {
			value.output = "6378168"
		}
		return cell, value, nil
	}
	return cell, value, &fixture.Diagnostic{Code: "42883", Message: "function " + name + "() does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}
}
