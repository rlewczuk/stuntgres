package functions

import (
	"strings"

	"stuntgres/internal/engine/expr"
)

// The engine's preflight and stored scalar SQL switches reach the array
// function nodes through these helpers until they adopt the node protocol.

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

func ArrayInspectSQL(e ArrayInspect, child func(expr.Expr) (string, error)) (string, error) {
	if err := e.Validate(); err != nil {
		return "", err
	}
	parts := make([]string, len(e.Args))
	for i, arg := range e.Args {
		text, err := child(arg)
		if err != nil {
			return "", err
		}
		parts[i] = text
	}
	return "pg_catalog." + e.Kind.Name() + "(" + strings.Join(parts, ", ") + ")", nil
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
func ArrayRandomSQL(e ArrayRandom, child func(expr.Expr) (string, error)) (string, error) {
	if err := e.Validate(); err != nil {
		return "", err
	}
	parts := make([]string, len(e.Args))
	for i, arg := range e.Args {
		text, err := child(arg)
		if err != nil {
			return "", err
		}
		parts[i] = text
	}
	return "pg_catalog." + e.Kind.Name() + "(" + strings.Join(parts, ", ") + ")", nil
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

func ArrayTransformSQL(e ArrayTransform, child func(expr.Expr) (string, error), operator func(string, string) string) (string, error) {
	if err := e.Validate(); err != nil {
		return "", err
	}
	parts := make([]string, len(e.Args))
	for i, arg := range e.Args {
		text, err := child(arg)
		if err != nil {
			return "", err
		}
		if e.Labels[i] != "" {
			text = "\"" + e.Labels[i] + "\" => " + text
		}
		parts[i] = text
	}
	if e.Operator {
		return "(" + parts[0] + " " + operator(e.Namespace, "||") + " " + parts[1] + ")", nil
	}
	return "pg_catalog." + e.Kind.Name() + "(" + strings.Join(parts, ", ") + ")", nil
}

func ArraySearchSQL(e ArraySearch, child func(expr.Expr) (string, error), operator func(string, string) string) (string, error) {
	parts := make([]string, len(e.Args))
	for i, arg := range e.Args {
		text, err := child(arg)
		if err != nil {
			return "", err
		}
		parts[i] = text
	}
	if e.Plan.Membership() {
		return "(" + parts[0] + " " + operator(e.Namespace, e.Plan.Name()) + " " + parts[1] + ")", nil
	}
	return "pg_catalog." + e.Plan.Name() + "(" + strings.Join(parts, ", ") + ")", nil
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
