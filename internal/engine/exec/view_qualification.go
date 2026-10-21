package exec

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// MapViewQualification applies mapper to a view qualification's private
// predicates and user predicate, copying only what changed.
func MapViewQualification(e plan.ViewQualificationExpr, mapper func(expr.Expr) (expr.Expr, bool, error)) (expr.Expr, bool, error) {
	changed := false
	var private []expr.Expr
	for i, x := range e.Predicates {
		next, different, err := mapper(x)
		if err != nil {
			return nil, false, err
		}
		if different {
			if private == nil {
				private = append([]expr.Expr(nil), e.Predicates...)
			}
			private[i] = next
			changed = true
		}
	}
	next, different, err := mapper(e.User)
	if err != nil {
		return nil, false, err
	}
	if different {
		e.User = next
		changed = true
	}
	if private != nil {
		e.Predicates = private
	}
	return e, changed, nil
}
