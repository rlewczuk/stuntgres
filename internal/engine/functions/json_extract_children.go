package functions

import (
	"stuntgres/internal/engine/expr"
)

// MapJSONExtractChildren serves the engine's preflight switch until it adopts
// the node protocol.

// MapJSONExtractChildren leaves the original immutable child slice untouched and
// only allocates when a child actually changes.
func MapJSONExtractChildren(e JSONExtract, mapChild func(expr.Expr) (expr.Expr, bool, error)) (JSONExtract, bool, error) {
	var args []expr.Expr
	for i, old := range e.Args {
		next, changed, err := mapChild(old)
		if err != nil {
			return e, false, err
		}
		if changed && args == nil {
			args = append([]expr.Expr(nil), e.Args...)
		}
		if args != nil {
			args[i] = next
		}
	}
	if args != nil {
		e.Args = args
	}
	return e, args != nil, nil
}
