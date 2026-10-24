package reference_test

import (
	"strings"

	"stuntgres/reference/fixture"
)

// ARRAYCOERCE is a leaf operation, not text reparsing of the whole array.
// These independently released leaf witnesses preserve a real NULL member;
// required-domain source arrays instead contain two non-NULL members.
func castArrayMatrixValue(cell castMatrixCell) castMatrixValue {
	source, target := castPhysicalFact(cell.source), castPhysicalFact(cell.target)
	value := castMatrixValue{input: cell.source.seed, output: cell.source.output}
	if !cell.contexts[2].admitted {
		return value
	}
	if target.element == "" {
		if target.category != "S" {
			panic("unclassified non-array destination for array source")
		}
		return value
	}
	if cell.contexts[2].operation == "identity/typmod" || cell.contexts[2].operation == "named-array-to-record-array" {
		return value
	}
	leafSource, leafTarget := castInventoryType(source.element), castInventoryType(target.element)
	leaf := castMatrixCell{source: leafSource, target: leafTarget}
	for i, ctx := range []byte{'i', 'a', 'e'} {
		leaf.contexts[i] = castReleasedPath(leafSource, leafTarget, ctx)
	}
	item := castScalarMatrixValue(leaf)
	encode := func(s string) string {
		array := castArrayText(s)
		if source.element == "cast_required" {
			atom := strings.TrimSuffix(strings.TrimPrefix(array, "{"), ",NULL}")
			return "{" + atom + "," + atom + "}"
		}
		return array
	}
	value.input, value.output = encode(item.input), encode(item.output)
	value.diagnostic = item.diagnostic
	if leafTarget.name == "cast_required" && source.element != "cast_required" {
		value.diagnostic = &fixture.Diagnostic{Code: "23502", Message: "domain cast_required does not allow null values", Schema: "public", DataType: "cast_required"}
	}
	return value
}
