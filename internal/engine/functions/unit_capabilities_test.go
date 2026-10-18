package functions

import "stuntgres/internal/engine/expr"

// unitCapabilities lets a test context embed the whole expr.Context method set
// while implementing only the capabilities the code under test calls. The
// embedded value stays nil, so an unexpected capability call panics.
type unitCapabilities = expr.Context

// unitArgs widens a slice of one fake argument type to the node argument type.
func unitArgs[T expr.Expr](args []T) []expr.Expr {
	out := make([]expr.Expr, len(args))
	for i, arg := range args {
		out[i] = arg
	}
	return out
}
