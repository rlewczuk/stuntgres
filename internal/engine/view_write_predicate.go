package engine

import (
	"stuntgres/internal/engine/expr"
)

func mapViewQualification(e viewQualificationExpression, mapper func(expr.Expr) (expr.Expr, bool, error)) (expr.Expr, bool, error) {
	changed := false
	var private []expr.Expr
	for i, x := range e.predicates {
		next, different, err := mapper(x)
		if err != nil {
			return nil, false, err
		}
		if different {
			if private == nil {
				private = append([]expr.Expr(nil), e.predicates...)
			}
			private[i] = next
			changed = true
		}
	}
	next, different, err := mapper(e.user)
	if err != nil {
		return nil, false, err
	}
	if different {
		e.user = next
		changed = true
	}
	if private != nil {
		e.predicates = private
	}
	return e, changed, nil
}
