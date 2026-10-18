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
	// argument labels are rejected and ResolutionOnly calls get the
	// missing-signature diagnostics, in that order.
	SharedGates bool
	Bind        func(b Binder, call parser.FunctionExpr) (expr.Expr, error)
}

// The static table is the registry: no init() registration. Names must not
// overlap any family that is still dispatched by the engine's name chain.
var definitions = [...]Definition{
	{Names: []string{"uuid_extract_timestamp", "uuid_extract_version"}, Bind: bindUUIDExtract},
	{Names: mathNames[:], ExactSpelling: true, SharedGates: true, Bind: bindMath},
}

// Lookup returns the definition that claims an unqualified function name.
func Lookup(name string) (Definition, bool) {
	for _, definition := range definitions {
		for _, candidate := range definition.Names {
			if candidate == name || !definition.ExactSpelling && strings.EqualFold(candidate, name) {
				return definition, true
			}
		}
	}
	return Definition{}, false
}
