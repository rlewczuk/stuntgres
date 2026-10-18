package functions

import (
	"stuntgres/internal/engine/expr"
)

// The engine's preflight switch reaches the array function nodes through
// these helpers until it adopts the node protocol.

func MapArrayInspectChildren(e ArrayInspect, child func(expr.Expr) (expr.Expr, bool, error)) (ArrayInspect, bool, error) {
	var args []expr.Expr
	for i, old := range e.Args {
		next, changed, err := child(old)
		if err != nil {
			return e, false, err
		}
		if changed {
			if args == nil {
				args = append([]expr.Expr(nil), e.Args...)
			}
			args[i] = next
		}
	}
	if args == nil {
		return e, false, nil
	}
	e.Args = args
	return e, true, nil
}

func MapArrayRandomChildren(e ArrayRandom, child func(expr.Expr) (expr.Expr, bool, error)) (ArrayRandom, bool, error) {
	var args []expr.Expr
	for i, old := range e.Args {
		next, changed, err := child(old)
		if err != nil {
			return e, false, err
		}
		if changed {
			if args == nil {
				args = append([]expr.Expr(nil), e.Args...)
			}
			args[i] = next
		}
	}
	if args == nil {
		return e, false, nil
	}
	e.Args = args
	return e, true, nil
}

func MapArrayTransformChildren(e ArrayTransform, child func(expr.Expr) (expr.Expr, bool, error)) (ArrayTransform, bool, error) {
	var args []expr.Expr
	// Simplification/preflight may evaluate immutable children and fail. Preserve
	// formal evaluation precedence there too, without changing written slots.
	for slot := range e.Args {
		for i, old := range e.Args {
			if int(e.Slots[i]) != slot {
				continue
			}
			next, changed, err := child(old)
			if err != nil {
				return e, false, err
			}
			if changed {
				if args == nil {
					args = append([]expr.Expr(nil), e.Args...)
				}
				args[i] = next
			}
		}
	}
	if args == nil {
		return e, false, nil
	}
	e.Args = args
	return e, true, nil
}

func MapArraySearchChildren(e ArraySearch, child func(expr.Expr) (expr.Expr, bool, error)) (ArraySearch, bool, error) {
	var args []expr.Expr
	for i, old := range e.Args {
		next, changed, err := child(old)
		if err != nil {
			return e, false, err
		}
		if changed {
			if args == nil {
				args = append([]expr.Expr(nil), e.Args...)
			}
			args[i] = next
		}
	}
	if args == nil {
		return e, false, nil
	}
	e.Args = args
	return e, true, nil
}
