package functions

import (
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// Definition registers one builtin family with the engine's function
// dispatch. Names are claimed case-insensitively unless ExactSpelling is set;
// Bind owns exact-spelling resolution, argument analysis and every diagnostic
// for a claimed call.
type Definition struct {
	Names []string
	// ExactSpelling claims only the exact (parser-folded) names, so other
	// spellings keep the engine's existing missing-function path.
	ExactSpelling bool
	// SharedGates asks the engine to apply its shared call gates before Bind:
	// argument labels are rejected, then ResolutionOnly calls get the
	// missing-signature diagnostics. On a case-insensitive definition, other
	// spellings of a claimed name are treated as ResolutionOnly.
	SharedGates bool
	// OwnsLabels exempts the definition from the shared label gate; Bind then
	// owns every argument-label diagnostic. It requires SharedGates.
	OwnsLabels bool
	// Claims, when set, is the engine chain's admission predicate for the
	// family and replaces Names matching; Names still lists every claimed
	// name. It keeps the chain's exact, folded or lowercased comparison.
	Claims func(name string) bool
	Bind   func(b Binder, call parser.FunctionExpr) (expr.Expr, error)
}

// The static table is the registry: no init() registration. Names must not
// overlap any family that is still dispatched by the engine's name chain.
var definitions = [...]Definition{
	{Names: []string{"uuid_extract_timestamp", "uuid_extract_version"}, Bind: bindUUIDExtract},
	{Names: jsonCallNames[:], Claims: jsonCallClaims, Bind: bindJSONCall},
	{Names: mathNames[:], ExactSpelling: true, SharedGates: true, Bind: bindMath},
	{Names: []string{"uuidv4"}, ExactSpelling: true, SharedGates: true, Bind: bindUUIDGeneration},
	{Names: []string{"uuidv7"}, ExactSpelling: true, SharedGates: true, OwnsLabels: true, Bind: bindUUIDGeneration},
	{Names: []string{"format"}, SharedGates: true, Bind: bindFormat},
	{Names: []string{"uuid_generate_v4"}, Bind: bindUUIDOssp},
	{Names: arrayInspectNames[:], Bind: bindArrayInspect},
	{Names: arrayRandomNames[:], Bind: bindArrayRandom},
	{Names: arrayTransformNames[:], Bind: bindArrayTransform},
	{Names: arraySearchNames[:], Bind: bindArraySearch},
	{Names: rangeInspectNames[:], Bind: bindRangeInspect},
	{Names: textObjectNames[:], ExactSpelling: true, SharedGates: true, Bind: bindTextObject},
	{Names: datetimeNames[:], ExactSpelling: true, SharedGates: true, Bind: bindDatetime},
	{Names: datetimeMakeNames[:], ExactSpelling: true, SharedGates: true, OwnsLabels: true, Bind: bindDatetime},
	{Names: []string{"like_escape"}, ExactSpelling: true, SharedGates: true, Bind: bindLikeEscape},
	{Names: stringNames[:], ExactSpelling: true, SharedGates: true, Bind: bindStringName},
	{Names: []string{"string_to_table"}, ExactSpelling: true, SharedGates: true, Bind: bindStringToTable},
	{Names: []string{"parse_ident"}, Bind: bindParseIdent},
	{Names: []string{"lower", "upper"}, Bind: bindLowerUpper},
}

// Lookup returns the definition that claims an unqualified function name.
func Lookup(name string) (Definition, bool) {
	for _, definition := range definitions {
		if definition.Claims != nil {
			if definition.Claims(name) {
				return definition, true
			}
			continue
		}
		for _, candidate := range definition.Names {
			if candidate == name || !definition.ExactSpelling && strings.EqualFold(candidate, name) {
				return definition, true
			}
		}
	}
	return Definition{}, false
}
